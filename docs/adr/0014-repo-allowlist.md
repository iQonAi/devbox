# ADR 0014 — Repo resolution: static allowlist in config, doubling as the security allowlist (D11)

**Status:** Accepted (approved 2026-06-18). **Amendment proposed:** ADR 0003
(D13, Proposed) keeps the registry as the allowlist but makes a repo an
*optional session property* and drops the ≥1-repo config requirement — not
yet accepted.

## Context

Tasks name repos by short name (`--repo devbox`), so something must map
names to `owner/repo`, default branch, and credentials. The design folds
two concerns into one structure: the *identity mapping* and the *security
boundary*. Because each registry entry binds a repo to its own
fine-grained, repo-scoped token (D3/ADR 0007;
`TECHNICAL_DESIGN.md:321`), the registry is simultaneously the complete
list of what the platform can touch on GitHub — a repo absent from the
registry is unreachable, with no code path that could reach it.

## Decision

- The registry is a static list in `config.yaml`: per entry `name`,
  `owner`, `repo`, `default_branch`, and `token_ref` — the LoadCredential
  secret *name*, never the token itself
  (`internal/config/config.go:50-58`, `config.example.yaml:11-16`).
- Validation is strict at load: at least one repo (the current global
  requirement ADR 0003 proposes dropping —
  `internal/config/config.go:116-118`), unique names, owner/repo present,
  and `token_ref` required per entry
  (`internal/config/config.go:128-141`).
- Every task path resolves through the registry and rejects unknown
  names: the daemon's submit (`internal/daemon/submit.go:37-46`) and the
  standalone CLI run (`cmd/agent-task/main.go:180-189`). There is no
  ad-hoc URL path in the daemon — `--repo-url` exists only on the
  standalone `run` command for local development
  (`cmd/agent-task/main.go:147`).
- The config file stays the source of truth: on startup the daemon seeds
  the registry into the store (`internal/daemon/daemon.go:182-197`), and
  the DB row carries the same `token_ref` name, never a secret
  (`internal/store/migrations/0001_init.sql:3-11`).
- Registration is a deliberate operator action: edit config, provision the
  matching repo-scoped token via `LoadCredential`
  (`deploy/systemd/agent-taskd.service:37-46`), restart. There is no API
  to add a repo.

## Consequences

- Blast radius is enumerable by reading one file: the platform can write
  to exactly the registered repos, each with its own least-privilege
  token. `agent-task repos` shows the live list
  (`cmd/agent-task/main.go:384-405`).
- Adding a repo costs an operator round-trip (config + token + restart) —
  friction that is the control working: nothing an agent produces can
  extend the allowlist.
- Task admission and secret resolution stay decoupled from GitHub: an
  unknown repo fails at submit time host-side, before any container or
  network activity (`internal/daemon/submit.go:44-46`).
- ADR 0003 (Proposed) shifts the registry's *coupling*, not its role: a
  repo becomes an optional property a session declares, but any declared
  repo must still resolve against this registry — no registry hit, no
  repo-bound work.
