# ADR 0002 — Session model: durable sandboxes, tasks executed inside them (D12)

**Status:** Proposed (drafted 2026-08-09 via #42; awaiting owner sign-off)

## Context

The shipped system has no unit of work larger than a container run. Task,
container, branch, workspace, memory scope, and success predicate are one
object with one lifetime: the runner's `Run` is a single atomic call — fresh
volume → create → copy in → start → collect → destroy — with teardown in a
`defer` (`internal/runner/podman.go:249`, `:268-271`); the agent's `HOME`
points at the per-task volume (`internal/runner/podman.go:35`) that dies in
the same call, so no memory survives between tasks; and a daemon restart
fails every non-terminal task and destroys its container
(`internal/daemon/daemon.go:160-181`).

Issue #42's outcomes ask for lifetimes that differ: a durable session
(outcomes 1–2), a session that picks up a set of work (3), and per-session
memory the agent builds across tasks (7). The architecture review on #42
proposed the session/task split; the owner's answers pinned the open
questions: sessions are **sandboxes first, not code-runners** (they may run
evals or security assessments with no git involvement at all), memory stays
per-session and lightweight, concurrency is left to sessions, and session
lifetime is literal.

On 2026-08-10 the owner added a direction comment on #42 pinning the
session's operating model: sessions should be durable the way a watcher loop
is durable — "a long running task that watches for changes and picks up the
work then works on it … Take the github abstraction out of it and you have a
devbox container session that's running performing tasks that it's assigned.
What they are and where they come from is abstracted away." The referenced
watcher is a host-side script (not repo code); its mechanics are the pattern
this ADR adopts: a durable outer loop with cheap idle polling, change
detection by comparing current state against a captured baseline, hand-off of
the delta when something changes, and a bounded lease under a supervisor
rather than an unsupervised immortal process.

Terms used here — **session**, **sandbox**, **work loop**, **one-shot run** —
are defined below; the repo has no `CONTEXT.md` glossary yet to hold them
(flagged on the PR, not silently added).

## Decision

- A **session** is a durable, named sandbox: one long-lived container plus
  one persistent volume, with its own config (ADR 0003), an **optional** repo
  binding, and per-session agent memory. The session model must not assume a
  repo — a session with no git involvement is a first-class case.
- Lifecycle is **create → work loop → close**. While open, the session runs
  a durable **work loop**: wait idle → notice an assigned task → pick it up →
  execute → report → return to idle. Tasks are units of work executed
  *inside* the session, each keeping its own id, events, artifacts, and
  terminal state; the session executes them **serially from its queue**.
  Task kinds and their outcomes are ADR 0004.
- The loop carries the watcher pattern's properties (owner direction,
  2026-08-10):
  - **Cheap idle wait** — an idle session costs a poll, not compute.
  - **Baseline/delta pickup** — the session picks up work by comparing the
    queue against what it has already handled and taking the delta; pickup is
    level-triggered on queue state, not edge-triggered on a missable
    notification, so assigned work survives restarts and races.
  - **Bounded lease under supervision** — the loop periodically proves
    liveness to the daemon and is re-armed, rather than being trusted to run
    unsupervised forever. The lease is the loop's health-check contract with
    the daemon, **not** a session lifetime bound: lifetime stays literal
    (below); a lease lapse means the daemon intervenes on a wedged loop, not
    that a healthy session expires.
- **Task sources are abstracted from the session.** The session's contract is
  "execute tasks assigned to it"; what the tasks are and where they come from
  is not the session's concern. The v1 source is explicit user assignment
  (CLI → daemon → session queue). Watcher-style sources — GitHub, filesystem,
  queues — are future sources that plug in on the daemon side without
  changing the session contract. Per D3, GitHub specifics stay host-side: a
  future GitHub watcher source lives in the host/daemon, never as a token or
  poller inside the container.
- **The assignment mechanism is an open question** for the implementation
  ADR/milestone: the daemon may push each queued task into the container via
  exec, or an in-container supervisor may pull from the session queue. The
  contract above (assigned tasks, serial execution, loop semantics) is the
  decided part; the owner's phrasing ("a long running task that watches …
  picks up the work") leans pull, but the load-bearing requirement is the
  source abstraction, and either mechanism satisfies the contract. The
  runner's sandbox exec primitive is needed under both.
- **Lifetime is literal:** a session stays alive until the user closes it.
  No max lifetime, no idle reaping in this version of the model. Lifetime
  bounds are named future work, not deferred defaults.
- **Memory is per-session:** it lives at the sandbox `HOME` on the session
  volume and survives across tasks. No cross-session or shared project
  memory yet — sharing is future work.
- The **one-shot run** remains as the degenerate case: create an ephemeral
  session → run one task → close. Today's `run`/`submit` behaviour is
  preserved through this path; for one-shot `code` runs the D4 export-in /
  `git bundle`-out transfer is unchanged.
- **git-flow inside a session is not controlled.** A long-running agent may
  create branches and sub-agents in its sandbox; devbox guarantees
  *extraction methods* (per task kind, ADR 0004), not a git workflow. D4 is
  preserved per `code` task, not imposed on the sandbox.
- **Restart reconciliation reattaches, not reaps:** the daemon reattaches to
  session containers by name after a restart; only in-flight tasks
  fail-and-recover. The current fail-and-destroy sweep
  (`internal/daemon/daemon.go:160-181`) stays correct only for ephemeral
  one-shot runs.
- The isolation invariants stay hard-coded in the runner
  (`internal/runner/runner.go:10-19`); nothing about durability may push a
  hardening flag out to a caller.
- **Concurrency is left to sessions** for now: no session-level concurrency
  caps in this version. D10's task limits keep governing task execution.

## Impact on D1–D11 (verified against the Decision Log)

| Decision | Verdict |
| --- | --- |
| D1 Go, D2 daemon+CLI/Unix socket, D3 host-only token, D5 rootless Podman, D7 adapter interface | **Hold.** D3 is reaffirmed: a durable session still gets no GitHub token. D5 holds but #17 (gVisor) gains priority — see R7. D7 holds with one seam: an optional capability interface for resume/continue — amend ADR 0001 rather than write a new one. |
| D4 full transfer model | **Holds, clarified:** applies per `code` task (one-shot runs unchanged); sessions do not control in-sandbox git-flow. |
| D6 base image | **Amendment proposed** via D13 (ADR 0003): per-session provisioning under the policy ceiling. |
| D8 task input | **Supersession proposed** via D14 (ADR 0004): input becomes kind + body + optional repo. |
| D9 success definition | **Supersession proposed** via D14 (ADR 0004): the kind owns the success predicate. |
| D10 limits | **Amendment proposed** via this ADR: task timeout and task concurrency stay for task execution; session residency is unbounded (literal lifetime) and session-level limits are deferred. |
| D11 repo allowlist | **Amendment proposed** via D13 (ADR 0003): registry stays the allowlist, but a repo is an optional session property and config must stop requiring ≥1 repo. |

## Security deltas (recorded, not waved through)

Extends the residual-risk table (R1–R5) in `TECHNICAL_DESIGN.md` §1.4:

- **R6 — persistent prompt-injection surface.** Hostile content read in task
  1 can write to session memory (`HOME`) and steer task 20. Mitigations in
  scope: memory is strictly per-session, a session is bound to the repo
  allowlist it was created with, and memory sits on a host-inspectable
  volume. A max-lifetime backstop was explicitly **not** adopted (lifetime is
  literal), so closing or resetting the session is the operator's lever.
- **R7 — breakout dwell time.** Disposability (§8.9) bounded a breakout to
  one task's lifetime; a session lives for days. This raises gVisor (#17)
  from a deferred nice-to-have to the mitigation for this specific change.
  D5 itself does not change.
- **R8 — restart reconciliation.** Reap-on-restart becomes data loss once
  sessions exist; reattach-by-name is a correctness *and* security
  requirement (an unaccounted-for running container is worse than a reaped
  one). Reattach must verify the container is the one the store recorded.

## Consequences

- The runner grows from a batch call into a sandbox lifecycle
  (create/exec/destroy) — exec is the runner primitive either assignment
  mechanism needs — with the one-shot run recomposed on top; the store needs
  a sessions record, a task→session link, and a **per-session task queue**
  the work loop consumes; the daemon needs an **assignment path**
  (submit-to-session) alongside today's one-shot submit; the pool needs
  per-session serialization; the loop needs an **event surface** (idle,
  pickup, report, lease renewal) so the CLI can show what a session is doing;
  and the agent adapter gains an optional resume/continue capability (an
  amendment to ADR 0001, not a new ADR). Sequencing per the #42 review.
- Container visibility (read-only log tail; PTY attach) is deliberately not
  part of this ADR — see ADR 0004's future-work note.
