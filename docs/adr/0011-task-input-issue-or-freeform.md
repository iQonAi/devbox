# ADR 0011 — Task input: a GitHub issue or free-form text (D8)

**Status:** Accepted (approved 2026-06-18).

> **Supersession pending:** ADR 0004 (D14, **Proposed** — not yet accepted)
> proposes superseding this decision. Task input would become **kind + body
> + optional repo binding**, with `--issue`/`--task` surviving as sugar for
> a `code` task. Until ADR 0004 is accepted, this ADR describes the shipped
> and operating behaviour.

## Context

The product's core workflow starts from a GitHub issue
(`docs/project/0001`, Core Workflow: "User submits repository + issue"),
but tying every task to an issue would force ceremony onto one-off work —
"fix the flaky test in CI" should not require filing an issue first. The
design resolved the gap by admitting exactly two input forms and making
them converge immediately: whichever form is used, the task becomes one
rendered prompt artifact, and everything downstream is identical
(`TECHNICAL_DESIGN.md:87`).

## Decision

- A task is created from **exactly one** of `--issue N` or `--task "…"`.
  The exclusivity is enforced three times — CLI submit
  (`cmd/agent-task/main.go:92-94`), CLI standalone run
  (`cmd/agent-task/main.go:163-168`), and the daemon API
  (`internal/daemon/submit.go:30-35`) — and once more at the prompt
  boundary, which rejects empty or ambiguous input
  (`internal/prompt/prompt.go:41-47`).
- For `--issue`, the **host** fetches the issue with `gh issue view --json`
  using the repo-scoped token (`internal/github/github.go:86-93`,
  `internal/controller/controller.go:176-189`) — the container never
  fetches anything (D3, ADR 0007). The issue's number, title, body, and
  URL render into the prompt; the issue title also names the feature
  branch slug (`internal/controller/controller.go:184-188`).
- Both forms render through one deterministic template whose instruction
  tail is byte-identical regardless of input form, so agent behaviour does
  not depend on how the task was supplied
  (`internal/prompt/prompt.go:31-37`, `:49-66`).
- The origin is recorded as `source` = `issue` | `task` on the task row
  (`internal/daemon/submit.go:87-90`,
  `internal/store/migrations/0001_init.sql:16`), and an issue-born task is
  back-linked: the host comments the PR URL on the source issue,
  best-effort (`internal/controller/controller.go:353-356`).

## Consequences

- Issue-driven and ad-hoc work share every downstream path — prompt,
  container, bundle, PR — so there is one pipeline to test, not two.
- The prompt tail hard-codes a code outcome ("implement the change …
  commit your work") for **every** task
  (`internal/prompt/prompt.go:31-37`). That assumption is exactly what ADR
  0004 (Proposed) found breaking on non-code tasks like "review PR #41":
  the input model can express only work that ends in commits.
- The `--issue` path is also a prompt-injection surface — issue bodies are
  third-party-authored text rendered straight into the prompt. The
  container's isolation (no token, no LAN egress) bounds the blast radius;
  session memory changes that calculus, recorded as R6 in ADR 0002
  (Proposed).
