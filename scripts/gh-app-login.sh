#!/bin/sh
# Log gh into GitHub as the justintanner-gc[bot] App, using the PEM
# supplied via the GH_APP_PEM env var (base64-encoded). Invoked from the
# Kamal post-deploy hook via `kamal app exec --reuse`, and safe to re-run
# manually at any time to refresh the installation token.
#
# Installation tokens expire ~60 minutes after issue, so running this
# once per deploy keeps the container authenticated for roughly an hour.
set -e

: "${GH_APP_PEM:?GH_APP_PEM not set in container env — check config/deploy.yml env.secret}"
: "${GH_APP_ID:?GH_APP_ID not set in container env — check config/deploy.yml env.clear}"
: "${GH_APP_INSTALLATION_ID:?GH_APP_INSTALLATION_ID not set in container env — check config/deploy.yml env.clear}"

PEM_FILE=$(mktemp)
trap 'rm -f "$PEM_FILE"' EXIT
printf '%s' "$GH_APP_PEM" | base64 -d > "$PEM_FILE"
chmod 600 "$PEM_FILE"

TOKEN=$(GH_APP_PEM_FILE="$PEM_FILE" /app/gascity/scripts/gh-app-token.sh)
if [ -z "$TOKEN" ]; then
    echo "!! Failed to mint GitHub App installation token" >&2
    exit 1
fi

printf '%s\n' "$TOKEN" | gh auth login --with-token
git config --global credential.helper store
printf 'https://x-access-token:%s@github.com\n' "$TOKEN" > "$HOME/.git-credentials"
chmod 600 "$HOME/.git-credentials"

echo "==> gh authenticated as GitHub App bot:"
gh auth status
