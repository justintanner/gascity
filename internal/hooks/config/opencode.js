// Gas City hooks for OpenCode (ESM server plugin).
// Installed by gc into {workDir}/.opencode/plugins/gascity.js
//
// Events:
//   session.created    → gc prime (load context)
//   session.compacted  → gc prime (reload after compaction)
//   session.deleted    → gc hook --inject (pick up work on exit)
//   chat.system.transform → prime context + gc nudge drain --inject + gc mail check --inject
//   session.compacting → inject recovery instructions
const log = process.env.GC_OPENCODE_DEBUG
  ? (...args) => console.error("[gascity]", ...args)
  : () => {};
export const server = async ({ $, directory }) => {
  log("plugin loaded, directory:", directory);
  let didInit = false;

  // Promise-based context loading ensures the system transform hook can
  // await the result even if session.created hasn't resolved yet.
  let primePromise = null;
  const captureRun = async (cmd) => {
    try {
      return await $`/bin/sh -lc ${cmd}`.cwd(directory).text();
    } catch (err) {
      console.error(`[gascity] ${cmd} failed`, err?.message || err);
      return "";
    }
  };

  const loadPrime = async () => {
    return await captureRun("gc prime --hook");
  };

  return {
    event: async ({ event }) => {
      if (event?.type === "session.created") {
        log("event: session.created");
        if (didInit) return;
        didInit = true;
        primePromise = loadPrime();
      }
      if (event?.type === "session.compacted") {
        log("event: session.compacted");
        primePromise = loadPrime();
      }
      if (event?.type === "session.deleted") {
        log("event: session.deleted");
        await captureRun("gc hook --inject");
      }
    },
    "experimental.chat.system.transform": async (input, output) => {
      log("system.transform called");
      // If session.created hasn't fired yet, start loading now.
      if (!primePromise) {
        primePromise = loadPrime();
      }
      const context = await primePromise;
      if (context) {
        output.system.push(context);
      } else {
        // Reset so next transform retries instead of pushing empty forever.
        primePromise = null;
      }
      // Per-turn nudge + mail injection.
      const nudges = await captureRun("gc nudge drain --inject");
      if (nudges) {
        output.system.push(nudges);
      }
      const mail = await captureRun("gc mail check --inject");
      if (mail) {
        output.system.push(mail);
      }
    },
    "experimental.session.compacting": async ({ sessionID }, output) => {
      log("session.compacting, sessionID:", sessionID);
      const role = process.env.GC_AGENT || "unknown";
      output.context.push(`
## Gas City Multi-Agent System

**After Compaction:** Run \`gc prime\` to restore full context.
**Check Hook:** \`gc hook\` - if work present, execute immediately.
**Agent:** ${role}
`);
    },
  };
};
