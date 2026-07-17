FROM node:24-bookworm-slim

# args
ARG BUN_VERSION=1.3.14
ARG GO_VERSION=1.26.5
ARG GO_SHA256_AMD64=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
ARG GO_SHA256_ARM64=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49
ARG PI_VERSION=latest 
ARG CODEX_VERSION=latest

ARG AGENT_USER=agent
ARG AGENT_UID=10001
ARG AGENT_GID=10001

USER root

# Set env 
ENV DEBIAN_FRONTEND=noninteractive \
   BUN_INSTALL="/home/${AGENT_USER}/.bun" \
   NPM_CONFIG_PREFIX="/home/${AGENT_USER}/.local" \
   PATH="/usr/local/go/bin:/home/${AGENT_USER}/.bun/bin:/home/${AGENT_USER}/.local/bin:${PATH}" \
   DISABLE_AUTOUPDATER=1

# Install Deps
RUN apt-get update \
   && apt-get install -y --no-install-recommends \
   build-essential \
   ca-certificates \
   bash \
   curl \
   fd-find \
   git \
   jq \
   procps \
   python3 \
   python3-pip \
   python3-venv \
   ripgrep \
   tini \
   unzip \
   xz-utils \
   && ln -s /usr/bin/fdfind /usr/local/bin/fd \
   && rm -rf /var/lib/apt/lists/*

# Install Golang
RUN arch="$(dpkg --print-architecture)" \
   && case "${arch}" in \
   amd64) \
   go_arch="amd64"; \
   GO_SHA256="${GO_SHA256_AMD64}" ;; \
   arm64) \
   go_arch="arm64"; \
   GO_SHA256="${GO_SHA256_ARM64}" ;; \
   *) exit 1 ;; \
   esac \
   && curl -fsSLo /tmp/go.tar.gz \
   "https://go.dev/dl/go${GO_VERSION}.linux-${go_arch}.tar.gz" \
   && printf '%s  %s\n' "${GO_SHA256}" /tmp/go.tar.gz | sha256sum -c - \
   && rm -rf /usr/local/go \
   && tar -C /usr/local -xzf /tmp/go.tar.gz \
   && rm /tmp/go.tar.gz \
   && go version


# non-root identity
RUN groupadd --gid "${AGENT_GID}" "${AGENT_USER}" \
   && useradd \
   --uid "${AGENT_UID}" \
   --gid "${AGENT_GID}" \
   --create-home \
   --shell /bin/bash \
   "${AGENT_USER}" \
   && mkdir -p \
   /workspace \
   /home/${AGENT_USER}/.local \
   /home/${AGENT_USER}/.bun \
   && chown -R \
   "${AGENT_UID}:${AGENT_GID}" \
   /workspace \
   /home/${AGENT_USER}

USER "${AGENT_USER}"

# Bun install to the agent user's home directory 
RUN curl -fsSL https://bun.sh/install \
   | bash -s "bun-v${BUN_VERSION}"

# Claude Code install
RUN curl -fsSL https://claude.ai/install.sh | bash

# CODEX & PI Installer
RUN npm install --global --ignore-scripts \
   "@earendil-works/pi-coding-agent@${PI_VERSION}" \
   && npm install --global \
   "@openai/codex@${CODEX_VERSION}"

# Fail the image build if anything is missing
RUN node --version \
   && npm --version \
   && bun --version \
   && go version \
   && python3 --version \
   && git --version \
   && rg --version \
   && fd --version \
   && claude --version \
   && pi --version \
   && codex --version \
   && test "$(id -u)" -ne 0

WORKDIR /workspace

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["bash"]
