# ADR 0007 — GitHub token location: host-only, never in the container (D3)

**Status:** Accepted (approved 2026-06-18)

## Context

The agent inside the container is untrusted code (`docs/project/0003`,
Security Philosophy): it can run arbitrary commands and its prompt may
carry hostile instructions. Any credential placed in the container must be
assumed exfiltrated. The GitHub token is the platform's highest-value
secret after the Podman control path — repo write access is exactly what a
hostile agent would want — while the model API key is the one secret the
agent genuinely cannot work without (it has to call the model), and it is
rotatable and infrastructure-free (`TECHNICAL_DESIGN.md:66`).

The first-draft architecture gave containers "agent credentials"
generically (`docs/project/0002`, Execution Containers). D3 hardened that:
the GitHub token never crosses the container boundary at all, and D4 (ADR
0008) removes the need for it to — the container never talks to a git
remote.

## Decision

- The token is used **only** in the host-side `github` package
  (`internal/github/github.go:1-7`) and the host-side mirror sync
  (`internal/repo/mirror.go:48`). It reaches `gh` via a scrubbed
  environment — any inherited `GH_TOKEN` is dropped first so the
  repo-scoped token is the single authoritative value
  (`internal/github/github.go:69-74`) — and reaches `git` via a one-shot
  credential helper reading an env var, never argv
  (`internal/github/github.go:24-26`, `internal/repo/mirror.go:17-21`),
  because `/proc/<pid>/cmdline` is world-readable
  (`internal/gitx/gitx.go:24-27`).
- The controller keeps the token out of the runner by construction: it is
  a field on the request consumed only to build the host-side GitHub
  client (`internal/controller/controller.go:164-169`); the container
  `Spec` carries a `SecretEnv` holding only the model credential
  (`internal/controller/controller.go:243-246`, `:250-261`).
- At rest the token is a systemd `LoadCredential` file, root-owned `0600`
  at the source, delivered read-only to the service user and unreadable by
  `agentbox`, the container-runner uid
  (`deploy/systemd/agent-taskd.service:33-44`); the daemon resolves it at
  submit time (`internal/daemon/submit.go:57-66`,
  `internal/creds/creds.go:19-47`).
- Nothing durable stores the secret: config and DB hold only a
  `token_ref` credential *name* (`internal/config/config.go:52-58`,
  `internal/store/migrations/0001_init.sql:10`).
- Tokens are fine-grained, repository-scoped, minted by one dedicated
  machine user (`iQonAi-Bot`), one per registered repo — the D11 registry
  (ADR 0014) binds each repo to its own `token_ref`
  (`TECHNICAL_DESIGN.md:321`).

## Consequences

- A compromised agent has no token to steal (`internal/github/github.go:6`);
  the worst GitHub-side outcome of a container breakout is bounded at zero,
  not at "one repo".
- All GitHub writes are host-mediated, which is what makes the PR approval
  gate enforceable: the platform's only outbound writes are branch push, PR
  open, and a best-effort issue back-link
  (`internal/controller/controller.go:334-356`). ADR 0004 (Proposed) builds
  on exactly this property when it scopes the PR gate to repo-linked `code`
  tasks.
- Durable sessions (ADR 0002, Proposed) reaffirm D3: a session container
  still gets no token, and any future GitHub watcher source lives
  host-side, never as a poller inside the container.
- The cost is that the agent cannot read GitHub context itself — a task
  like "review PR #41" needs host-side resolution of references into the
  prompt (ADR 0004's future-work note), because the container has no
  credential to fetch with. That friction is the design working as
  intended.
