# ADR 0012 — Success definition: agent exit 0 + at least one commit; tests informational (D9)

**Status:** Accepted (approved 2026-06-18).

> **Supersession pending:** ADR 0004 (D14, **Proposed** — not yet accepted)
> proposes superseding this decision. The success predicate would move to
> the task **kind**: for `code` tasks, exit 0 with zero commits becomes
> Completed-with-no-PR instead of Failed; `plain` tasks succeed on exit 0
> with their report collected. The "tests informational" half survives the
> proposal. Until ADR 0004 is accepted, this ADR describes the shipped and
> operating predicate.

## Context

The platform needs a machine-checkable definition of "the task worked"
that does not require trusting the agent's own claims. Two observable
facts are available at extraction time: the agent process's exit code, and
whether the bundle applied any commits. Test results are deliberately
**not** part of the definition: the PR is the only human approval gate
(`docs/project/0001`, Human Approval Model), so a reviewer — not the
platform — judges whether failing tests sink the change. Failing a task
for failing tests would also discard useful partial work that a reviewer
might want to see.

## Decision

- **Completed** iff the agent exited `0` **and** at least one commit
  applied to the feature branch
  (`internal/controller/controller.go:324-329`). The commit count comes
  from the host-side bundle apply, not from anything the agent reports
  (`internal/controller/controller.go:315-321`,
  `internal/repo/apply.go:44-53`); it is stored per task
  (`internal/store/migrations/0001_init.sql:24`).
- Everything else is **Failed** with the reason recorded: non-zero exit or
  zero commits (`internal/controller/controller.go:325-327`), an
  unappliable bundle (`internal/controller/controller.go:314-320`),
  timeout (`internal/controller/controller.go:45-56`), or a push/PR
  failure — publish errors downgrade the task while leaving the commits on
  the local branch for inspection
  (`internal/controller/controller.go:330-350`). User cancellation is
  **Cancelled**, distinguished from failure by the context's cancel cause
  (`internal/controller/controller.go:36-56`).
- Publication is gated on success: only a Completed task pushes and opens
  a PR (`internal/controller/controller.go:329-334`).
- **Tests are informational.** The PR body reserves a test-results section
  (`internal/github/prbody.go:37-41`) and its closing marker tells the
  reviewer the change is agent-produced and review is required
  (`internal/github/prbody.go:43-45`). No test outcome feeds the
  predicate. Note honestly: the pipeline does not yet *run* tests — the
  container wrapper runs the agent, bundles, and captures the diff only
  (`internal/controller/wrapper.go:16-30`), and the PR body is currently
  built without test output (`internal/controller/controller.go:343-346`).
  The design intends test capture as an attached artifact
  (`TECHNICAL_DESIGN.md:254`); "informational" is the decided posture,
  the capture itself has not shipped.

## Consequences

- Success is cheap to evaluate and hard for the agent to fake: both inputs
  (exit code, applied commit count) are measured host-side.
- The predicate encodes "every task ends in commits." That is the
  assumption ADR 0004 (Proposed) dismantles — a task that correctly
  produces zero commits (a review, an eval) cannot succeed today, which is
  the recorded motivation for the pending supersession.
- Tests-as-information keeps the human review gate authoritative (D3/ADR
  0007 makes it enforceable; ADR 0004 keeps it for `code` tasks) and keeps
  the platform out of the business of interpreting test frameworks.
