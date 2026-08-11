# ADR 0013 — Resource limits: 2 concurrent tasks, 30-minute timeout, both overridable (D10)

**Status:** Accepted (approved 2026-06-18). **Amendment proposed:** ADR 0002
(D12, Proposed) scopes these limits to *task execution* — session residency
would be unbounded (literal lifetime) with session-level limits deferred —
not yet accepted.

## Context

Agent runaway is a named residual risk (R5, `TECHNICAL_DESIGN.md:77`): an
agent can loop forever, burning wall-clock, model spend, and a worker
slot. The VM is fixed-size — 8 vCPU / 16 GB
(`docs/project/0002`, Devbox VM) — so concurrency must be capped where
tasks are admitted, and every task needs a hard upper bound on lifetime.
The numbers are sized so two concurrent tasks fit comfortably in 16 GB
(`TECHNICAL_DESIGN.md:296`); both are config-overridable because they are
capacity tuning, not security invariants.

## Decision

- **Concurrency: 2** by default (`internal/config/config.go:16`),
  overridable via `limits.max_concurrent`, validated ≥ 1
  (`internal/config/config.go:124-126`). Enforcement is structural: the
  daemon starts a fixed worker pool of exactly that many goroutines
  draining a bounded queue of 128 (`internal/daemon/daemon.go:102`,
  `internal/pool/pool.go:30-50`). Submission never blocks the socket
  handler — a full queue is an error, not a stall
  (`internal/pool/pool.go:52-68`).
- **Timeout: 30 minutes** wall-clock by default
  (`internal/config/config.go:17`), overridable via `limits.task_timeout`,
  validated as a duration (`internal/config/config.go:120-122`). The
  deadline wraps the **entire task**, not just the container run, so a
  wedged git or `gh` call cannot hold a worker slot forever
  (`internal/daemon/daemon.go:78-85`). A timeout fires the same cancel
  path as a user cancel and lands as `Failed` with reason `timeout`
  (`internal/controller/controller.go:45-56`; §7.4).
- **Per-container caps** back the limits inside the sandbox: CPU, memory,
  and pids are applied unconditionally by the runner when set
  (`internal/runner/podman.go:46-54`). Their values are currently in-code
  literals — 2 CPUs / 2048 MB / 256 pids
  (`internal/daemon/submit.go:128`, `cmd/agent-task/main.go:281`) — not
  config; ADR 0003 (Proposed) records promoting them to real config under
  the policy ceiling as a prerequisite for per-session limits.
- Teardown is bounded too: daemon shutdown drains in-flight work for at
  most 30 seconds before abandoning it, so a wedged task cannot hang
  shutdown (`internal/daemon/daemon.go:30-35`, `:144-146`).

## Consequences

- A runaway agent costs at most one worker slot for 30 minutes and its
  container's capped CPU/memory/pids; the other slot stays live.
- The whole-task deadline means host-side phases (mirror sync of a huge
  repo, a slow PR create) spend the same budget as the agent itself — one
  knob, coarse by design.
- Queue depth 128 is a hard-coded admission bound
  (`internal/daemon/daemon.go:102`); beyond it, submits fail fast and the
  client retries — acceptable single-user behaviour.
- Durable sessions change what "concurrent" counts: ADR 0002 (Proposed)
  keeps these limits governing task execution while an idle session costs
  a poll, not a slot — the amendment leaves this ADR's numbers intact and
  narrows their scope.
