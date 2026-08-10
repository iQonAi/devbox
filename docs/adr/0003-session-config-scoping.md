# ADR 0003 — Config scoping: per-session config under a host policy ceiling (D13)

**Status:** Proposed (drafted 2026-08-09 via #42; awaiting owner sign-off)

## Context

Configuration is one global, process-wide struct — image, podman command,
limits, and agents all daemon-scoped (`internal/config/config.go:24-33`) —
and validation hard-requires at least one registered repo
(`internal/config/config.go:115-118`), which alone blocks a repo-less
session (issue #42, outcome 5). The CLI enforces the same assumption
(`cmd/agent-task/main.go:89-90`, `:169-170`). Meanwhile the per-container
resource caps (cpus, memory, pids) are literals in code, not config
(`internal/daemon/submit.go:128`, `cmd/agent-task/main.go:281`);
`limits.task_timeout` and `limits.max_concurrent` are real applied config
(`internal/daemon/submit.go:117`, `internal/daemon/daemon.go:102`).

Issue #42's goals make the session the configured object: the user decides
its networking, agent, name, and packages per session. That turns session
config into **untrusted input** — a user-supplied document that must never
be able to weaken the sandbox — so it needs a host-side bound.

## Decision

- Config splits into two layers:
  1. **Daemon config** (operator-owned file): socket path, data dir, podman
     command, the repo registry (D11), and the **policy ceiling**.
  2. **Session config** (user-supplied at session create): agent, network
     policy, provisioning, resource limits, optional repo binding —
     validated against the ceiling before the sandbox is created.
- The **policy ceiling** is the host-set bound the session config cannot
  exceed: network policy (the egress deny-list of §8.5 is a floor, never
  weakenable per session), runtime, mounts, and resource caps. Validation is
  reject-on-exceed, not clamp-silently.
- The ceiling bounds *configuration*; the isolation invariants stay
  hard-coded in the runner (`internal/runner/runner.go:10-19`) and are not
  expressible in any config layer at all.
- **Repo becomes an optional session property, not a required global.**
  `Validate` stops requiring ≥1 repo (`internal/config/config.go:116-118`);
  the registry remains the security allowlist (D11) that any repo binding a
  session declares must resolve against. No registry hit, no repo-bound
  work — but a repo-less session is valid.
- The current in-code limit literals become real config under the ceiling —
  a prerequisite, since per-session limits cannot scope config that does not
  exist.
- **Amends D6:** the batteries-included base image stays the global default;
  per-session provisioning (packages, setup) is expressed in session config
  and bounded by the ceiling. The mechanism (setup script vs per-session
  images) is *not* decided here — it puts user-supplied code at session
  start and needs its own security review.

## Extension path (intended, not built)

- **Named session profiles** (operator-defined presets a user picks from)
  slot between the two layers with no schema break: a profile is just a
  stored session config validated against the same ceiling.
- Per-repo token scoping (D3/D11) is untouched: token refs stay in the
  daemon layer; no config layer ever holds a secret value.

## Consequences

- `config.yaml` stops being the description of "the task" and becomes the
  description of the host: registry + ceiling. What a sandbox looks like
  moves to session create time.
- Session config is an attack surface and is treated like one: validated
  against the ceiling, rejected loudly, and recorded (auditable) per session.
- One-shot runs (ADR 0002) need a default session config so today's CLI
  keeps working with no new flags.
