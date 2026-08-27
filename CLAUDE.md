# agent-task

## Project: Agent-Task

Self-hosted platform for running coding agents in isolated, disposable execution
environments. One task = one branch = one container; agents are treated as
untrusted code; pull requests are the only human approval gate. Single-user V1.

Requirements live in `docs/project/` (vision, architecture, security/threat model).
The agreed technical design and milestones are in `TECHNICAL_DESIGN.md` — read it
before implementing. Key approved decisions (see its Decision Log, D1–D11):

- **Language:** Go (single static binary `agent-task`, `serve` runs the daemon).
- **Architecture:** always-on daemon + thin CLI over a Unix socket (no TCP).
- **Isolation:** rootless Podman + gVisor; the untrusted container shares **no git
  repo and no GitHub token** with the host — source is copied in, commits come out
  as a `git bundle` the host applies onto an `agent/*` feature branch.
- **GitHub:** token is host-only; host pushes the branch and opens the PR.
- **Runtime stack in the agent image:** Node/TS + Bun, Go, Python 3.
- **Agents:** Claude Code, then Pi (Codex deferred).
- **Persistence:** SQLite.

Milestones M0–M5 and the M6 pi adapter have shipped. Build milestone by milestone, per
the TECHNICAL_DESIGN milestones; do not write code ahead of an approved milestone.

## Agent skills

### Issue tracker

Issues and PRDs are tracked as GitHub issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default triage vocabulary — `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
