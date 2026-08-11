# ADR 0006 — Process architecture: daemon + thin CLI over a Unix socket, no TCP (D2)

**Status:** Accepted (approved 2026-06-18)

## Context

Two requirements pull in opposite directions. Long-running agent tasks must
survive the user walking away — "long-running tasks require keeping a local
machine online" is a named pain point (`docs/project/0001`, Problem
Statement) — which demands an always-on server process. But that process is
the crown jewel of the threat model: it holds the Podman control path and
the GitHub tokens (risk R3, `TECHNICAL_DESIGN.md:75`), so every network
surface it exposes is surface an attacker can reach. OS access to the VM is
already gated by Tailscale+SSH (`docs/project/0003`, Tailscale Model), so an
OS session is an authentication boundary the platform gets for free.

## Decision

- One always-on daemon (`agent-task serve`) owns the store, the mirror
  cache, the worktrees, the runner, and all secrets; the CLI is a thin
  client that talks to it and exits.
- The transport is a **Unix socket only** — the daemon listens with
  `net.Listen("unix", …)` (`internal/daemon/daemon.go:209`) and no TCP
  listener exists anywhere in the codebase. The client hard-wires its HTTP
  transport to dial the socket regardless of URL
  (`internal/client/client.go:30-32`).
- Access control is file permissions, not application auth: the socket is
  chmod `0660` (`internal/daemon/daemon.go:27`, `:216`), owner the service
  user, group `agent-taskd` which the operator joins; the socket lives in a
  systemd `RuntimeDirectory` because a rootless service cannot own a bare
  `/run/*.sock` (`deploy/systemd/agent-taskd.service:22-27`). Reaching the
  socket at all requires an OS session — the Tailscale+SSH gate is the auth
  boundary, and the platform adds no second credential system.
- The protocol over the socket is plain HTTP/JSON with a versioned route
  table (`internal/api/server.go:44-52`); the default path is
  `/run/agent-task/agent-task.sock` (`internal/config/config.go:11`).
- Tasks belong to the daemon, not the client connection: work runs on the
  daemon's worker pool (`internal/daemon/daemon.go:102`), so a client
  disconnect does not touch a running task.
- Startup defends the socket rather than assuming it: a leftover socket
  file is removed only after a dial proves nothing is listening — a live
  daemon is refused, not stolen from (`internal/daemon/daemon.go:226-249`).

## Consequences

- The daemon has zero network attack surface; R3's mitigation is
  structural, not a firewall rule. Remote use is "SSH in, run the CLI" — no
  web UI or remote API is possible without revisiting this ADR.
- The CLI works only on the box (or through SSH); every CLI verb is a
  small HTTP call (`internal/client/client.go`), so future clients (a bot,
  a TUI) reuse the same socket API.
- Multi-user separation is the OS group, which is adequate exactly because
  V1 is single-user (`docs/project/0001`, Non-Goals); a multi-user version
  would need real per-caller identity on the socket.
- Sessions (ADR 0002, Proposed) keep this shape: the assignment path is
  CLI → daemon → session queue, and the daemon side of the socket is where
  future task sources plug in.
