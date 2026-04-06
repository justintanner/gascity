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

# --- Hand off PID 1 to the CMD (dashboard) ---
exec "$@"
