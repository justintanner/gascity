# Dispatching Work

`gc sling` routes work to agents. Three modes:

## Direct dispatch (bead to agent)

```
gc sling <agent> <bead-id>             # Route a bead to an agent's hook
```

The agent receives the bead on its hook and runs it per GUPP.

**Rig-scoped beads:** `gc sling` automatically resolves the rig directory
for rig-scoped bead IDs (e.g. `hw-abc`) and runs `bd update` from there,
so the rig's `.beads` database is found without manual intervention.

## Formula dispatch (formula on agent)

```
gc sling <agent> -f <formula>          # Run a formula, creating a molecule
```

Creates a molecule from the formula and hooks the root bead to the agent.

## Wisp dispatch (formula + existing bead)

```
gc sling <agent> <bead-id> --on <formula>  # Attach formula wisp to bead
```

Creates a molecule wisp on the bead and routes to the agent.

## Formulas

```
gc formula list                        # List available formulas
gc formula show <name>                 # Show formula definition
```

### Built-in formulas

**mol-do-work** — Simple work lifecycle. Agent reads the bead, implements
the solution in the current working directory, and closes the bead.
No git branching, no worktree isolation, no refinery handoff. Good for
demos and simple single-agent workflows.

```
gc sling <agent> <bead-id> --on mol-do-work
```

**mol-polecat-commit** — Direct-commit variant. Creates a worktree but
commits directly to base_branch with no feature branch or refinery step.
Includes preflight tests, implementation, and self-review quality gates.
For small installations where merge review is unnecessary.

```
gc sling <agent> <bead-id> --on mol-polecat-commit
```

**mol-polecat-base** — Shared base for polecat work formulas. Defines
the common steps (load context, preflight, implement, self-review) that
variant formulas extend. Not typically used directly — use a variant
like mol-polecat-commit or mol-polecat-work instead.

### Gastown pack formulas (work variants)

These require the gastown pack. They extend the built-in
`mol-polecat-base`.

**mol-polecat-work** — Feature-branch variant. Creates a worktree and
feature branch, implements, then pushes and reassigns to the refinery
for merge review. Production default for multi-agent setups.

```
gc sling <agent> <bead-id> --on mol-polecat-work
```

**mol-polecat-work-reviewed** — Human-reviewed variant. Like
mol-polecat-work but adds investigation, planning, and a human
checkpoint before implementation. The polecat writes a plan to bead
notes, notifies the mayor, and blocks until approved. Use for
high-risk or complex work that needs human sign-off before coding.

```
gc sling <agent> <bead-id> --on mol-polecat-work-reviewed
```

### Gastown pack formulas (patrol loops)

Patrol formulas are auto-poured by agent startup prompts — you typically
don't sling these manually:

- **mol-refinery-patrol** — Refinery merge loop (check for work, merge one branch, repeat)
- **mol-witness-patrol** — Rig work-health monitor (orphan recovery, stuck polecats, help mail)
- **mol-deacon-patrol** — Controller sidekick (work-layer health, system diagnostics)
- **mol-digest-generate** — Periodic activity digest mailed to the mayor
- **mol-shutdown-dance** — Due process for stuck agents (interrogate → execute → epitaph)

## Convoys (grouped work)

```
gc convoy create <name> <bead-ids...>  # Group beads into a convoy
gc convoy list                         # List active convoys
gc convoy status <id>                  # Show convoy progress
gc convoy add <id> <bead-ids...>       # Add beads to convoy
gc convoy close <id>                   # Close convoy
gc convoy check <id>                   # Check if all beads done
gc convoy stranded                     # Find convoys with no progress
gc convoy autoclose                    # Close convoys where all beads done
```

## Automations

```
gc automation list                     # List automation rules
gc automation show <name>              # Show automation definition
gc automation run <name>               # Manually trigger an automation
gc automation check <name>             # Check if gate conditions are met
gc automation history <name>           # Show automation run history
```
