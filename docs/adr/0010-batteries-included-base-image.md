# ADR 0010 — Base image: one batteries-included image for every agent and task (D6)

**Status:** Accepted (approved 2026-06-18). **Amendment proposed:** ADR 0003
(D13, Proposed) keeps this image as the global default but adds per-session
provisioning under the host policy ceiling — not yet accepted.

## Context

The agent works offline from the host's point of view: it gets a source
copy and an isolated network whose only reachable services are the public
internet (ADR 0008, ADR 0009). Whatever toolchain a task needs must either
already be in the image or be installable by the agent at task time
through the egress-filtered network. A minimal image would push toolchain
installs into every task run — slow, flaky, and repeated per task, since
containers are disposable and never reused (`TECHNICAL_DESIGN.md:309`).
The decision targets Node/TypeScript, Go, and Python as the supported
stacks (`TECHNICAL_DESIGN.md:38`), so the image carries all three plus
the agent CLIs. (Inference: the source docs do not name the registered
repos' languages; the stack list is the decision's own scope, not an
observation about the registry.)

## Decision

- One global image serves every agent and every task. Contents
  (`images/base/Dockerfile`): `node:24-bookworm-slim` base (`:1`), Bun
  (`:85-86`), Go installed from the official tarball with sha256
  verification (`:46-62`), Python 3 with pip/venv, plus git, ripgrep, fd,
  jq, curl, build-essential, and tini (`:25-43`).
- All three agent CLIs are baked in — Claude Code (`:89`), pi and Codex
  (`:92-95`) — even though Codex has no adapter yet (D7, ADR 0001): the #7
  amendment made adapter staging a host-side software question with no
  image work per agent.
- The image is built non-root: a dedicated `agent` user, uid `10001`
  (`images/base/Dockerfile:66-82`), which the runner pins as the container
  user — the uid is a fixed isolation invariant, not configurable
  (`internal/runner/runner.go:11`).
- The build fails unless every tool answers and the build user is
  non-root: a smoke-test layer checks each version and `id -u`
  (`images/base/Dockerfile:98-109`).
- The image is **not** an adapter concern: adapters map commands and env
  vars, never images (ADR 0001; `TECHNICAL_DESIGN.md:305`). The daemon
  applies one configured image to every task
  (`internal/config/config.go:19`, `config.example.yaml:19`).
- Rebuild cadence is manual for V1 (OQ-2, `TECHNICAL_DESIGN.md:103`);
  the build/update process is documented in `images/README.md`.

## Consequences

- Task startup cost is a container create, not a toolchain install; agent
  CLI versions are pinned per image build rather than drifting per task.
- The image is large and shared: every task carries all three runtimes
  whether it needs them or not — the accepted trade for a single-user
  platform with known repos.
- One image to patch: updating an agent CLI or toolchain is one rebuild
  plus the smoke-test gate, and every subsequent task picks it up
  (containers are never reused, ADR 0009).
- A task whose repo needs a runtime the image lacks depends on the agent
  installing it in-container at task time, repeated every run — this gap
  is what ADR 0003's per-session provisioning amendment (Proposed) is for.
