# ADR 0004 — Task kinds: non-code outcomes, success owned by the kind (D14)

**Status:** Proposed (drafted 2026-08-09 via #42; awaiting owner sign-off)

## Context

Two live failures on #42 ("do a pr review on pull request #41", "address the
PR comments…") failed regardless of what the agent did, because a code
outcome is hard-wired at three layers: the terminal-outcome predicate —
Completed iff exit 0 **and** ≥1 commit (D9,
`internal/controller/controller.go:325`); the prompt tail, which tells every
task to implement a change and commit it (`internal/prompt/prompt.go:31-37`);
and the container wrapper, which hard-codes `git bundle` / `git diff`
extraction (`internal/controller/wrapper.go:16-31`). A task that correctly
produces zero commits cannot succeed, and a task with no repo cannot even be
expressed.

Sessions (ADR 0002) are sandboxes first: evals and security assessments
involve no git at all. The owner also pinned the approval-gate question:
"PR review gates everything" was only ever true because push-branch +
open-PR is the platform's sole outbound write
(`internal/controller/controller.go:334`) — the property is real, but it is
a property of **repo-based tasks**, not of all session activity.

**Task kind** and **`plain` task** are new terms; the repo has no
`CONTEXT.md` glossary yet to hold them (flagged on the PR).

## Decision

- Every task has a **kind**: `code` or `plain`. The kind owns the prompt
  contract, the in-container outcome extraction (today's wrapper becomes
  kind-owned), and the **success predicate**.
- **`code`** — repo-bound. Source export in, `git bundle` out, feature
  branch, PR: the D4 pipeline, unchanged per task. **Commits are a property
  of a `code` outcome, not the definition of success**: exit 0 with zero
  commits is Completed with no PR, not Failed (`commit_count` is already in
  the schema — `internal/store/migrations/0001_init.sql:24`). This
  supersedes D9.
- **`plain`** — no repo required. The result is **artifacts and a summary in
  the drop-dir, nothing else**: no branch, no push, no PR, no host-mediated
  side effects. Success is the agent exiting 0 with its report collected.
- Task input becomes **kind + body + optional repo binding** (via the
  session, ADR 0003), superseding D8's issue-or-free-form pair. The existing
  `--issue` / `--task` flags survive as sugar for a `code` task.
- **The PR gate is scoped, not weakened:** "pull requests are the only human
  approval gate" holds for repo-linked `code` tasks — the only path where
  the platform writes to GitHub. `plain` tasks write nothing outside their
  drop-dir, so there is nothing to gate. Any future host-mediated outbound
  action (e.g. the platform posting a PR comment on the agent's behalf)
  narrows this property for real and requires its own ADR, a default-deny
  allowlist, and a security review — explicitly not decided here.

## Future work (named, not built)

- **Container visibility: read-only log tail** is the direction — the
  meaningful want per the owner (rated P2–P3, not in scope here). Live PTY
  attach into a running sandbox is deferred further: it is a new inbound
  channel to untrusted code. Today `status` prints neither `error` nor
  artifacts (`cmd/agent-task/main.go:134`) and no `logs` command exists;
  that observability gap is increment 0 of the #42 review, not this ADR.
- **GitHub context resolver (inbound, read-only).** A `plain` "review PR
  #41" task still cannot *see* the PR: the host's only GitHub read is issue
  fetch (`internal/github/github.go:86`) and D3 keeps the token host-only.
  Host-side resolution of PR/issue references into the prompt is the
  designed answer and its own piece of work (it feeds third-party-authored
  text into prompts — injection surface, security review required).

## Consequences

- Non-code work becomes representable end to end: predicate, prompt, and
  extraction all dispatch on the kind instead of assuming a commit.
- The wrapper (`internal/controller/wrapper.go`) moves from the controller
  to kind ownership, finishing the move PR #43 started for summaries.
- The store needs `kind` on the task record plus real outcome persistence;
  a `plain` task's artifacts are its only output, so losing them is losing
  the result.
- Tests remain informational for `code` tasks (D9's second half survives the
  supersession): PR review stays the gate for code landing anywhere.
