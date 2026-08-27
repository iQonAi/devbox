# ADR 0001 — Agents in V1: Claude Code + Pi behind one adapter interface (D7)

**Status:** Accepted (approved 2026-06-18; amended 2026-07-17 via #7, 2026-08-08 via #34)

## Context

Agent-Task runs coding agents as untrusted code inside disposable containers
(D4 full-transfer model). Each agent has its own CLI, auth mechanism,
non-interactive invocation, and transcript format. The system needs more than
one agent to prove the abstraction isn't shaped around a single vendor, but
every additional adapter carries research, hardening, and validation cost —
so V1 sequences them.

Two amendments moved the original plan:

- **#7** put all agent CLIs (Claude Code, Codex, pi) into the single
  batteries-included base image (D6), so adapter staging is purely a
  host-side software question — no image work per agent.
- **#34** promoted **pi** over Codex for M6: pi is open and multi-provider,
  which exercises the adapter seams (auth env var per provider,
  machine-readable transcript, exit-code semantics) harder than a second
  closed single-provider CLI would.

## Decision

- **Claude Code** is the first adapter (M3). **Pi** is the second (M6) and
  proves the abstraction. **Codex** is deferred; its adapter is staged after
  pi.
- All agents sit behind one 3-method Go interface (`internal/agent`):
  `Name()`, `EnvVar(method)`, `Command(method, promptPath, transcriptPath)`.
  The adapter owns prompt passing, completion detection (Command must exit
  non-zero when the agent fails), and transcript placement (§8.8). The
  container image is **not** an adapter concern — D6's base image is global.
- A registry (`adapters` map) backs `Lookup`; the adapter-contract parity
  suite iterates the registry, so registering an agent without contract
  coverage fails CI.
- **Auth:** `AuthMethod` is `subscription` (OAuth token) or `api_key`
  (provider key); `EnvVar` maps `(agent, method)` to the single credential
  env var injected into the container. Pi supports only `api_key` — its
  subscription auth is an interactive login flow with no documented env
  injection.
- **Provider selection for multi-provider agents (pi):** the adapter pins a
  provider (`ANTHROPIC_API_KEY` + `--provider anthropic`), and the security
  model enforces it — exactly one credential crosses the container boundary,
  so the agent cannot reach any other provider regardless of its fallback
  logic.

## Extension path (intended, not built)

- **A second pi provider** is a second registry name (e.g. `pi-openai`)
  mapped to a provider-parameterized constructor: zero interface change, zero
  config-schema change, one parity-suite row (enforced by the registry
  coverage check). Do **not** add a `provider:` config field until a real
  second-provider use case exists.
- **Codex (or any new agent)** is a new adapter file + one registry line +
  one parity row + a `config.example.yaml` entry. If its transcript needs a
  third bespoke failure guard, that is the signal to extract a shared guard
  helper — not before.

## Consequences

- Adding an agent is an enforced checklist, not a design exercise.
- Transcript **shape** varies per agent (claude: single JSON object; pi:
  JSONL event stream) while the artifact name (`transcript.json`) is fixed —
  consumers must not assume a shape (see #36 for the summary-extraction
  follow-up this caused).
- Per-agent behavioral guarantees (exit-on-failure, flag validity) cannot be
  fully asserted at the unit seam; fixtures cover the guard logic (#37) and
  the VM HITL run validates the real CLI per milestone.
