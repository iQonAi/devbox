# ADR 0005 — Implementation language: Go, one static binary (D1)

**Status:** Accepted (approved 2026-06-18)

## Context

The devbox is a single-user V1 platform running on one VM: an always-on
orchestrator daemon plus a thin CLI (D2), driving git, Podman, and the `gh`
CLI as subprocesses, persisting to SQLite (`TECHNICAL_DESIGN.md` §4–§6). The
owner's directive for the whole design is "simple and maintainable over
framework-heavy" (`TECHNICAL_DESIGN.md:23`).

The source requirements (`docs/project/0001`–`0003`) do not name a language;
the choice was made at design approval. The recorded decision is the shape —
"Go (single static binary)" (`TECHNICAL_DESIGN.md:33`) — and the rationale
below the shape is inference from what the code observably relies on, marked
as such.

## Decision

- The platform is one Go module (`go.mod:1`, Go 1.26 — `go.mod:3`) producing
  one binary, `agent-task`, whose subcommands cover both roles: `serve` runs
  the daemon, everything else is the thin client
  (`cmd/agent-task/main.go:37-51`). Deployment is copying that binary to the
  VM (`deploy/systemd/agent-taskd.service:20`).
- Internal packages map one-to-one to the components of the architecture doc
  (`TECHNICAL_DESIGN.md` §4): `config`, `controller`, `repo`, `runner`,
  `agent`, `github`, `store`, `prompt`, plus the seams that shipped alongside
  (`api`, `client`, `pool`, `creds`, `gitx`, `daemon`).
- Dependencies stay minimal and pure-Go: the SQLite driver is
  `modernc.org/sqlite` (`internal/store/store.go:7`), a cgo-free driver, so
  the static-binary property survives using a C-heritage database
  (inference: the design records "single static binary", and a cgo driver
  would break exactly that).
- Everything that is not orchestration is a subprocess, not a library
  binding: git via `internal/gitx/gitx.go:34`, Podman via
  `internal/runner/podman.go:89-98`, GitHub via the `gh` CLI
  (`internal/github/github.go:64-83`). The stdlib carries the rest — `net/http`
  over the Unix socket (`internal/daemon/daemon.go:110`), `log/slog` for
  structured logs (`cmd/agent-task/main.go:369`), `database/sql`
  (`internal/store/store.go:4`).

## Consequences

- One artifact to build, version, and install; the daemon and CLI cannot
  drift apart because they are the same binary.
- Goroutines + `context` give the concurrency model the lifecycle needs —
  the worker pool (`internal/pool/pool.go:30-50`) and per-task
  cancellation/timeout (`internal/pool/pool.go:124`) are stdlib constructs,
  no framework.
- The subprocess posture means the binary depends on `git`, `gh`, and
  `podman` existing on the host — the runbook owns provisioning them; the
  binary itself stays portable.
- No scripting-language escape hatch: every behaviour change is a compile
  and redeploy. Accepted for a single-user platform.
