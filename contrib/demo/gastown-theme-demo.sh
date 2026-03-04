#!/usr/bin/env bash
# gastown-theme-demo.sh — Demonstrate hot-reskin: apply gastown theming to live agents.
#
# Creates a temp city with 4 agents (sleep 3600), starts them bare,
# then applies gastown tmux theming live — no restart needed.
#
# Usage:
#   ./gastown-theme-demo.sh
#
# Env vars:
#   GC_SRC — gascity source tree (default: /data/projects/gascity)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GC_SRC="${GC_SRC:-/data/projects/gascity}"
# shellcheck source=narrate.sh
source "$SCRIPT_DIR/narrate.sh"

DEMO_DIR="/tmp/gc-theme-demo"
PACK_DIR="$GC_SRC/examples/gastown/packs/gastown"

# ── Cleanup on exit ──────────────────────────────────────────────────────
cleanup() {
    (cd "$DEMO_DIR" 2>/dev/null && gc stop 2>/dev/null) || true
    rm -rf "$DEMO_DIR"
}
trap cleanup EXIT

# ── Clean slate ──────────────────────────────────────────────────────────
rm -rf "$DEMO_DIR"

narrate "Gastown Theme Demo" --sub "Hot-reskin: apply theming to live agents without restart"

# ── Step 1: Create temp city with 4 bare agents ─────────────────────────

step "Creating temp city at $DEMO_DIR..."

gc init "$DEMO_DIR"

# Create a tiny git repo as a rig.
DEMO_RIG="$DEMO_DIR/demo-rig"
mkdir -p "$DEMO_RIG"
(cd "$DEMO_RIG" && git init -q && git commit --allow-empty -q -m "init")

# Register the rig.
(cd "$DEMO_DIR" && gc rig add "$DEMO_RIG") || true

# Write city.toml with 4 agents (no theming, just sleep).
DEMO_RIG_ABS=$(cd "$DEMO_RIG" && pwd)
cat > "$DEMO_DIR/city.toml" <<EOF
[workspace]
name = "theme-demo"

[providers.shell]
command = "sleep"
args = ["3600"]
prompt_mode = "none"

# City-scoped agents
[[agents]]
name = "mayor"
provider = "shell"

[[agents]]
name = "deacon"
provider = "shell"

# Rig-scoped agents
[[rigs]]
name = "demo-rig"
path = "$DEMO_RIG_ABS"

[[agents]]
name = "witness"
dir = "demo-rig"
provider = "shell"

[[agents]]
name = "polecat-1"
dir = "demo-rig"
provider = "shell"
EOF

step "City config written (4 agents, no theming)"

# ── Step 2: Start agents ────────────────────────────────────────────────

step "Starting agents..."
(cd "$DEMO_DIR" && gc start)

step "Agents are up (plain tmux sessions, no theme)"
echo ""
echo "  Sessions:"
(cd "$DEMO_DIR" && gc status 2>/dev/null) || true
echo ""

pause "Press Enter to apply gastown theme to all live sessions..."

# ── Step 3: Apply gastown theming live ──────────────────────────────────

narrate "Applying Theme" --sub "Running tmux-theme.sh and tmux-keybindings.sh on live sessions"

# Session names match agent names (per-city socket isolation).
SESSIONS=("mayor" "deacon" "demo-rig--witness" "demo-rig--polecat-1")
AGENT_NAMES=("mayor" "deacon" "demo-rig/witness" "demo-rig/polecat-1")

# Discover the tmux socket for this city.
SOCKET=$(cd "$DEMO_DIR" && gc config get workspace.name 2>/dev/null || echo "theme-demo")

for i in "${!SESSIONS[@]}"; do
    sess="${SESSIONS[$i]}"
    name="${AGENT_NAMES[$i]}"
    step "Theming '$name' (session: $sess)..."
    GC_TMUX_SOCKET="$SOCKET" "$PACK_DIR/scripts/tmux-theme.sh" "$sess" "$name" "$PACK_DIR" || true
    GC_TMUX_SOCKET="$SOCKET" "$PACK_DIR/scripts/tmux-keybindings.sh" "$PACK_DIR" || true
done

step "Theme applied to all agents!"
echo ""
echo "  Attach to any session to see the themed status bar:"
echo ""
for s in "${SESSIONS[@]}"; do
    echo "    tmux -L $SOCKET attach -t $s"
done
echo ""
echo "  Keybindings: prefix+n (next), prefix+p (prev), prefix+g (agent menu)"
echo ""

pause "Press Enter to clean up and exit..."

# ── Done ─────────────────────────────────────────────────────────────────

narrate "Demo Complete" --sub "Theming applied to live sessions — no agent restarts"
