# ADR 0008 — Commit transfer: full transfer model, copy in / bundle out (D4)

**Status:** Accepted (approved 2026-06-18)

## Context

The first-draft architecture mounted the host worktree into the container
(`docs/project/0002`, Execution Containers: "Receive: worktree mount").
That shape has a known escape class: a hostile agent writes to `.git` —
hooks, config, aliases — and the *host* executes it the next time any host
git command touches that repo. Reviewing agent output cannot catch this
reliably; the class had to be removed, not mitigated
(`TECHNICAL_DESIGN.md:66`).

D4 is the removal: the untrusted container shares **no git repository**
with the host. The host worktree still exists, but it is host-side only —
the container gets a self-contained copy, and commits come back as data.

## Decision

- **In:** the host builds a standalone export — a real clone with
  `--no-hardlinks` (no shared inodes with the mirror) and
  `--single-branch`, with its `origin` remote removed so the copy holds no
  URL to fetch from or push to (`internal/repo/export.go:18-33`). The
  runner copies it into the container's own volume at `/task/src` and the
  rendered prompt to `/task/prompt.md` (`internal/runner/podman.go:286-297`,
  paths fixed in `internal/runner/runner.go:12-15`).
- **Inside:** the agent works and commits locally. The container command
  wrapper sets a local git identity, runs the agent, then — agent-agnostic —
  bundles `base..HEAD` into `/task/out/changes.bundle` and captures the
  diff (`internal/controller/wrapper.go:16-30`); `base` is the export's tip
  commit, recorded by the host before launch
  (`internal/repo/export.go:38-44`).
- **Out:** artifacts leave via `podman cp` from the drop-dir as inert data
  (`internal/runner/podman.go:314-321`). The host never executes anything
  from it: only regular files are collected — `podman cp` preserves
  symlinks, so a symlinked artifact could alias an arbitrary host path and
  is skipped (`internal/controller/controller.go:389-398`), and a
  non-regular `changes.bundle` fails the task outright
  (`internal/controller/controller.go:304-311`).
- **Apply:** the host verifies the bundle, fetches from it, and
  fast-forwards the feature branch — a bundle that does not descend from
  base is rejected, never force-applied (`internal/repo/apply.go:23-53`).
- Every host-side git invocation runs hook-disabled and config-isolated as
  a second layer: `core.hooksPath=/dev/null`, `gc.auto=0`, a from-scratch
  environment with `HOME=/nonexistent`, `GIT_CONFIG_NOSYSTEM=1`, and
  terminal prompts off (`internal/gitx/gitx.go:32`, `:53-67`) — so even the
  host's *own* repos are handled as if their config were hostile.

## Consequences

- The `.git`-hook escape class does not exist here: there is no
  agent-writable `.git` that any host git command ever reads or executes
  (`TECHNICAL_DESIGN.md:275`). This, with D3 (ADR 0007), is what makes the
  container boundary a real trust boundary rather than a convenience.
- The cost is per-task I/O: every task clones a fresh export (risk R4,
  `TECHNICAL_DESIGN.md:76`) — accepted for known repos on a 200 GB SSD,
  revisit for large monorepos (OQ-4).
- The agent cannot push, fetch, or see the real branch names; its world is
  the copy. The prompt tells it so (`internal/prompt/prompt.go:31-37`).
- Sessions (ADR 0002, Proposed) keep D4 per `code` task rather than
  imposing it on the sandbox: a long-lived session may run any git-flow it
  likes inside its copy — devbox guarantees the extraction method, not the
  workflow. ADR 0004 (Proposed) makes that extraction kind-owned, with the
  bundle pipeline unchanged for `code` tasks.
