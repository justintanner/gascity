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
if [ ! -d /gc/nakedapi ]; then
    if [ -n "${GH_APP_PEM:-}" ]; then
        /app/gascity/scripts/gh-app-login.sh || echo "warning: GitHub auth failed; trying public clone"
    fi
    echo "Cloning nakedapi rig..."
    git clone https://github.com/justintanner/nakedapi.git /gc/nakedapi \
        || echo "warning: nakedapi clone failed; register rig later via kamal shell"
fi

# --- Bootstrap or resume /gc workspace ---
# /gc is a Kamal-managed Docker volume, so anything COPYed into /gc in
# the image is shadowed at runtime. On first boot we let `gc init`
# create the city tree AND spawn a background supervisor that registers
# the city. On subsequent boots the tree already exists but no
# supervisor is running (containers don't preserve processes across
# restarts), so we start one explicitly and register the city.
if [ ! -d /gc/.gc ]; then
    echo "Bootstrapping /gc workspace from baked prod-city.toml..."
    gc init \
        --file /app/gascity/config/prod-city.toml \
        --skip-provider-readiness \
        /gc
    # `gc init` has now started a supervisor in the background and
    # registered /gc with it. Nothing more to do before the dashboard.
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
