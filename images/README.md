# Agent images

Container images for the Agent-Task platform. Today this is the single **agent base
image** (`base/`) that disposable execution containers run from (design D6,
§8.7). One task = one container, started from this image; the agent inside is
treated as untrusted code.

The image is batteries-included and **non-root**: it carries the language
toolchains, common CLI essentials, and the agent CLIs, so a task container needs
nothing fetched at run time.

## Contents (`base/Dockerfile`)

| Tool        | Version (pinned)              | Source                                   |
| ----------- | ----------------------------- | ---------------------------------------- |
| Node        | 24 LTS                        | `node:24-bookworm-slim` base image       |
| Bun         | `BUN_VERSION` (1.3.14)         | official `bun.sh` install script         |
| Go          | `GO_VERSION` (1.26.5)          | go.dev tarball, **sha256-verified**      |
| Python      | 3.11 + pip + venv             | Debian `apt`                             |
| Essentials  | git, ripgrep (`rg`), fd, build-essential, curl, jq, unzip, procps, xz-utils | `apt` |
| Claude Code | latest (installer-tracked)    | native installer (`claude.ai/install.sh`) |
| Codex       | `CODEX_VERSION` (latest)       | `@openai/codex` (npm)                    |
| Pi          | `PI_VERSION` (latest)          | `@earendil-works/pi-coding-agent` (npm)  |

Agents (D7): **Claude Code**, **Codex**, and **Pi** (open, multi-provider — Claude,
OpenAI, Copilot). Pi was promoted from "deferred" to included at #7; its runner
*adapter* is still staged later (Claude M3, Codex M6).

Runtime identity: non-root user `agent` (uid/gid **10001**). `tini` is PID 1
(signal forwarding + zombie reaping); default workdir `/workspace`.

> Runtime hardening (`--cap-drop ALL`, `--security-opt no-new-privileges`,
> read-only rootfs, isolated + egress-locked network, resource caps) is applied
> by the **runner** at container launch (M2, §8.7), not baked into the image.

## Build

Build **rootless**, as the container-runner user (`agentbox`), with DNS pinned to
a public resolver — the host egress deny-list blocks internal resolvers, so the
build needs the public one (see the runbook / #4):

```bash
cd images/base
sudo -u agentbox env HOME=/home/agentbox XDG_RUNTIME_DIR=/run/user/999 \
  podman build --dns 9.9.9.9 -t agent-task-base:dev .
```

The final `RUN` is a **smoke-test gate**: it invokes every tool (`node`, `npm`,
`bun`, `go`, `python3`, `git`, `rg`, `fd`, `claude`, `pi`, `codex`) and asserts
the process is non-root. A successful build therefore *is* the validation — a
missing or broken tool fails the build.

## Updating (OQ-2)

Rebuild cadence is **manual for V1** — no scheduled/automatic rebuilds. To update:

- **Toolchain / agent versions:** bump the `ARG`s at the top of the Dockerfile
  (`BUN_VERSION`, `GO_VERSION`, `PI_VERSION`, `CODEX_VERSION`), or override per
  build (`--build-arg PI_VERSION=0.80.10`). For reproducible/release builds,
  **pin** the npm-installed agent CLIs (Pi, Codex) to explicit versions rather
  than `latest`. **Claude Code** installs via its native installer
  (`claude.ai/install.sh`), which always tracks the latest release — it is not
  ARG-pinnable; switch it to the `@anthropic-ai/claude-code` npm package if a
  pinned Claude version is ever required.
- **Go checksum (pinned):** `GO_VERSION` is verified against per-arch pinned
  hashes (`GO_SHA256_AMD64` / `GO_SHA256_ARM64`). When you bump `GO_VERSION`,
  update **both** from `https://go.dev/dl/go<version>.linux-<arch>.tar.gz.sha256`
  (a wrong/stale hash fails the build at the `sha256sum -c` step).
- Rebuild and let the smoke-test gate confirm.

## Reference build

First green build (2026-07-17) confirmed all tools via the smoke-test gate:
node 24.18.0, npm 11.16.0, bun 1.3.14, go 1.24.5, python 3.11.2, git 2.39.5,
ripgrep 13.0.0, fd 8.6.0, claude 2.1.212, pi 0.80.10, codex-cli 0.144.5,
non-root uid 10001.

Since that build, `GO_VERSION` was bumped to **1.26.5** and Go sha256
verification was added — reconfirm with one rebuild.
