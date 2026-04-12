#!/bin/sh
set -e

# --- Git + Dolt identity ---
# Both git (agents) and dolt (bd beads backend) need an identity. Fall
# back to a safe default so local docker runs work without env wiring;
# Kamal overrides these via env.clear in config/deploy.yml.
: "${GIT_USER:=gascity}"
: "${GIT_EMAIL:=gascity@localhost}"
git config --global user.name "$GIT_USER"
git config --global user.email "$GIT_EMAIL"
git config --global credential.helper store
dolt config --global --add user.name "$GIT_USER" >/dev/null 2>&1 || true
dolt config --global --add user.email "$GIT_EMAIL" >/dev/null 2>&1 || true

# --- Claude Code + Fireworks AI ---
# Claude Code reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL from the
# environment. When using Fireworks AI as the backend, we derive the key
# from the canonical FIREWORKS_AI_API_KEY secret (injected via Kamal
# env.secret) and point all model env vars at the Fireworks Kimi endpoint
# so that *any* `claude` invocation in the container — not just
# gc-managed sessions — uses Fireworks by default.
if [ -n "${FIREWORKS_AI_API_KEY:-}" ]; then
    export ANTHROPIC_API_KEY="$FIREWORKS_AI_API_KEY"
    export ANTHROPIC_BASE_URL="${ANTHROPIC_BASE_URL:-https://api.fireworks.ai/inference}"
    export ANTHROPIC_MODEL="${ANTHROPIC_MODEL:-accounts/fireworks/models/kimi-k2p5}"
    export ANTHROPIC_SMALL_FAST_MODEL="${ANTHROPIC_SMALL_FAST_MODEL:-accounts/fireworks/models/kimi-k2p5}"
    export ANTHROPIC_DEFAULT_SONNET_MODEL="${ANTHROPIC_DEFAULT_SONNET_MODEL:-accounts/fireworks/models/kimi-k2p5}"
    export ANTHROPIC_DEFAULT_HAIKU_MODEL="${ANTHROPIC_DEFAULT_HAIKU_MODEL:-accounts/fireworks/models/kimi-k2p5}"
    export ANTHROPIC_DEFAULT_OPUS_MODEL="${ANTHROPIC_DEFAULT_OPUS_MODEL:-accounts/fireworks/models/kimi-k2p5}"
fi

# --- Claude Code user settings ---
# Claude Code's `skipDangerousModePermissionPrompt` is honored only in
# USER-scope settings (~/.claude/settings.json); project-scope settings
# loaded via `--settings` silently drop it. Without it, every new
# `claude --dangerously-skip-permissions` session hits a one-time
# "enter bypass mode?" confirmation that autonomous tmux agents can't
# answer, leaving them stuck in normal-permission mode.
#
# Seed the user settings file on every boot so Claude trusts the flag
# from the start. Merge-safe: if the file exists, only add the key when
# missing so we don't clobber user edits made via `kamal shell`.
CLAUDE_USER_SETTINGS="${HOME:-/home/agent}/.claude/settings.json"
mkdir -p "$(dirname "$CLAUDE_USER_SETTINGS")"
if [ ! -f "$CLAUDE_USER_SETTINGS" ]; then
    echo '{"skipDangerousModePermissionPrompt": true}' > "$CLAUDE_USER_SETTINGS"
    echo "Seeded $CLAUDE_USER_SETTINGS"
elif ! grep -q 'skipDangerousModePermissionPrompt' "$CLAUDE_USER_SETTINGS" 2>/dev/null; then
    tmp="$(mktemp)"
    jq '. + {skipDangerousModePermissionPrompt: true}' "$CLAUDE_USER_SETTINGS" > "$tmp" \
        && mv "$tmp" "$CLAUDE_USER_SETTINGS" \
        && echo "Patched skipDangerousModePermissionPrompt into $CLAUDE_USER_SETTINGS"
fi

# --- 1Password CLI readiness ---
if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ]; then
    if op whoami >/dev/null 2>&1; then
        echo "1Password CLI authenticated (service account)"
    else
        echo "warning: OP_SERVICE_ACCOUNT_TOKEN set but op authentication failed"
    fi
else
    echo "warning: OP_SERVICE_ACCOUNT_TOKEN not set; op will not be available"
fi

# --- Clone rig repos into /gc volume ---
# apicity rig — public repo, no auth needed for the clone. GH App auth
# is still used at agent runtime so endpoint-builder can `gh pr create`
# under the bot identity. On first boot we clone; on every subsequent
# boot we hard-reset to origin/main so the container always runs the
# latest pack/code shipped to apicity's main branch.
if [ ! -d /gc/apicity/.git ]; then
    if [ -n "${GH_APP_PEM:-}" ]; then
        /app/gascity/scripts/gh-app-login.sh || echo "warning: GitHub auth failed; trying public clone"
    fi
    echo "Cloning apicity rig..."
    git clone https://github.com/justintanner/apicity.git /gc/apicity \
        || echo "warning: apicity clone failed; register rig later via kamal shell"
else
    echo "Pulling apicity main..."
    git -C /gc/apicity fetch origin main \
        && git -C /gc/apicity reset --hard origin/main \
        || echo "warning: apicity pull failed; continuing with existing checkout"
fi

# --- Bootstrap or resume /gc workspace ---
# /gc is a Kamal-managed Docker volume, so anything COPYed into /gc in
# the image is shadowed at runtime. On first boot `gc init` creates the
# city tree, starts a supervisor, and registers the city (the systemctl
# error from `gc supervisor install` is cosmetic — it falls back to a
# direct start). On subsequent boots we start the supervisor ourselves.
if [ ! -d /gc/.gc ]; then
    echo "Bootstrapping /gc workspace from baked prod-city.toml..."
    # gc init starts a supervisor internally (falls back to direct start
    # when systemctl is unavailable). Redirect stderr so the cosmetic
    # "systemctl not found" message doesn't look like a real failure.
    gc init \
        --file /app/gascity/config/prod-city.toml \
        --skip-provider-readiness \
        /gc 2>&1
else
    echo "Resuming existing /gc workspace..."
    gc supervisor run &
    SUPERVISOR_PID=$!
    echo "Supervisor started in background (PID $SUPERVISOR_PID)"

    # Wait up to ~10s for the supervisor API to come up.
    i=0
    while [ "$i" -lt 20 ]; do
        if curl -fsS http://127.0.0.1:8372/health >/dev/null 2>&1; then
            break
        fi
        i=$((i + 1))
        sleep 0.5
    done

    if ! gc start /gc; then
        echo "warning: gc start /gc failed (continuing; fix via kamal shell)"
    fi
fi

# --- Lock down beads directory ---
# The .beads directory holds all bead state (dolt repos). Restrict access
# to the container user so agents cannot tamper with it directly.
if [ -d /gc/.beads ]; then
    chmod 700 /gc/.beads
fi

# --- Hand off PID 1 to the CMD (dashboard) ---
exec "$@"
