# Production image for gascity, deployed via Kamal 2.
# Build locally with: kamal build push
# Or standalone:      docker build -t gascity:local .
FROM docker/sandbox-templates:claude-code

ARG GO_VERSION=1.25.6

USER root

# System dependencies. gascity needs: tmux + git (session provider, agents),
# jq (pack scripts), pgrep/procps + lsof (process discovery), dolt + bd
# (default "bd" beads backend), tini (PID 1), curl (healthcheck), ripgrep,
# zsh, gh (agent tools), libicu-dev (beads ICU linking).
RUN apt-get update && apt-get install -y \
    build-essential \
    git \
    sqlite3 \
    tmux \
    curl \
    ripgrep \
    zsh \
    gh \
    netcat-openbsd \
    tini \
    vim \
    jq \
    procps \
    lsof \
    libicu-dev \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/*

# Install Go from the official tarball. apt golang-go is too old for
# gascity's go.mod (requires Go 1.25.0+).
RUN ARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" \
    | tar -C /usr/local -xz
ENV PATH="/app/gascity/bin:/usr/local/go/bin:/usr/local/bin:/home/agent/go/bin:/home/agent/bin:${PATH}"

# Install beads (bd) from source — prebuilt binaries link against an
# older ICU than the base image ships. Matches gastown's approach.
RUN GOBIN=/usr/local/bin CGO_ENABLED=1 go install \
    github.com/steveyegge/beads/cmd/bd@latest \
    && go clean -cache -modcache

# Install dolt (SQL database engine used by the bd beads backend).
RUN curl -fsSL https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash

# Install the Codex CLI. The base image ships `claude` but not `codex`;
# npm is already present in the claude-code base image.
RUN npm install -g @openai/codex

# Install opencode. Same npm pattern as codex above. prod-city.toml sets
# provider = "opencode" against Fireworks AI; docker-entrypoint.sh writes
# opencode's auth.json + model.json at container start from
# $FIREWORKS_AI_API_KEY (injected via Kamal env.secret).
RUN npm install -g opencode-ai

# Create app + workspace directories. /gc is a Kamal volume mount at
# runtime, so anything baked here gets shadowed — bootstrapping happens
# in docker-entrypoint.sh.
RUN mkdir -p /app /gc && chown -R agent:agent /app /gc

# Shell PATH for interactive `kamal app exec --interactive` sessions.
RUN echo 'export PATH="/app/gascity/bin:/usr/local/bin:/usr/local/go/bin:$PATH"' > /etc/profile.d/gascity.sh && \
    echo 'export PATH="/app/gascity/bin:/usr/local/bin:/usr/local/go/bin:$PATH"' >> /etc/zsh/zshenv && \
    echo 'export TERM="xterm-256color"' >> /etc/profile.d/term.sh && \
    echo 'export COLORTERM="truecolor"' >> /etc/profile.d/colorterm.sh

USER agent

# Source tree — also contains the baked prod-city.toml and the gastown
# pack that city.toml references via absolute path.
COPY --chown=agent:agent . /app/gascity

# Build gc. The binary lands at /app/gascity/bin/gc; PATH (set above)
# already includes /app/gascity/bin so `gc` is globally invokable.
RUN cd /app/gascity && make build

# Entrypoint must be readable/executable for the `agent` user; copy
# after the source tree so .dockerignore still excludes it from the
# earlier blanket COPY.
COPY --chown=agent:agent docker-entrypoint.sh /app/docker-entrypoint.sh

WORKDIR /gc

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=90s --retries=3 \
  CMD curl -fsS http://localhost:8080/up || exit 1

ENTRYPOINT ["tini", "--", "/app/docker-entrypoint.sh"]
CMD ["gc", "dashboard", "serve", "--api", "http://127.0.0.1:8372", "--port", "8080"]
