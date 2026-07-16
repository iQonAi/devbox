# Agent Devbox — Technical Design (V1)

> Status: **Design baseline — decisions approved 2026-06-18.** The 11 architectural decisions in §1.1 are settled and drive this document. Implementation has not started; this is the agreed design that code will be reviewed against.
>
> Source requirements:
> - `docs/project/0001-vision-product-requirements.md`
> - `docs/project/0002-system-architecture.md`
> - `docs/project/0003-security-threat-model.md`
>
> Anything still open is labelled **[OPEN]**. Anything chosen as a sensible default (low-stakes, owner did not object) is labelled **[DEFAULT]**.

---

## 0. Design principles (from the requirements, load-bearing)

1. **One task = one branch = one container.** (0002) — note the worktree is now *host-side only*; see §1.1 D4.
2. **Containers are disposable and least-trusted.** (0002, 0003)
3. **Pull requests are the only human approval gate.** Agents never merge/deploy. (0001, 0003)
4. **No production credentials, no Proxmox/TrueNAS/Jellyfin/home-LAN/personal-workstation/SSH-keys.** (0003)
5. **Repository-scoped, fine-grained GitHub tokens via one dedicated machine user.** (0003)
6. **Single-user V1.** Discord, memory, orchestration, scheduling, multi-agent: *designed for, not built.* (0001, 0002)
7. **Simple and maintainable over framework-heavy.** (owner directive)

---

## 1. Architecture review

### 1.1 Approved decisions (the design baseline)

| # | Decision | Choice | Drives |
|---|----------|--------|--------|
| D1 | Implementation language | **Go** (single static binary) | all |
| D2 | Process architecture | **Daemon + thin CLI**, Unix-socket only (no TCP) | §5 |
| D3 | GitHub token location | **Host-only.** The untrusted container never receives the token | §9, §10 |
| D4 | Commit transfer | **Full transfer model.** No shared git repo with the container: host copies source *in*, agent commits locally, host extracts a `git bundle` *out* and applies onto the feature branch host-side | §8, §9 |
| D5 | Container runtime | **Rootless Podman** (ships now); **gVisor (`runsc`) validated-deferred** — runner kept swappable so it can flip on later (amended 2026-06-23, see note below) | §8, §10 |
| D6 | Base image | **Batteries-included**: Node/TS + **Bun**, Go, Python 3, plus common essentials | §8.7 |
| D7 | Agents in V1 | **Claude Code** (M3) + **Codex** (M6). Pi designed-for, deferred | §8.8, §12 |
| D8 | Task input | **Issue or free-form** (`--issue N` / `--task "…"`) | §7, §9 |
| D9 | Success definition | **Agent exit 0 + ≥1 commit.** Tests run and attach to the PR but are informational | §7.3 |
| D10 | Resource limits | **2 concurrent tasks, 30-min per-task timeout** (both config-overridable) | §5, §8.6 |
| D11 | Repo resolution | **Static allowlist** in `config.yaml` (short name → owner/name/default-branch/token-ref). Doubles as the security allowlist | §6, §9 |

**D5 amendment (2026-06-23, issues #4 / #17).** The isolation spine that ships first is **rootless Podman + the egress deny-list** (the egress control is the highest-severity protection — R2 — and is independent of the runtime). **gVisor (`runsc`)** moves from a day-one default to a *validated-deferred* layer: its overhead/compat and the cost of flipping the swappable runner over to it are measured in a follow-up spike (#17), and it is adopted as the default only if that move is cheap. Rootful Docker was explicitly rejected — the rootless property (a breakout lands unprivileged) is the point, and rootless Podman keeps it while supporting `runsc` first-class.

Approved low-stakes defaults:

- **[DEFAULT]** Branch naming `agent/<agent>/<issue-or-slug>-<shortid>` — the `agent/` prefix reserves the namespace for future GitHub branch-protection/rulesets (part of the product vision, not built in V1).
- **[DEFAULT]** Secrets at rest: root-owned `0600` files injected via systemd `LoadCredential`; never written to images, the transferred source, or the DB.
- **[DEFAULT]** One dedicated GitHub machine user across all registered repos.
- **[DEFAULT]** Logs/artifacts kept indefinitely on disk in V1; a `prune` command is a documented future add.
- **[DEFAULT]** PR body templated: task id, agent, issue link, summary, test results, "🤖 agent-produced — review required."

### 1.2 What the requirements get right (kept)

- The **1:1:1 task/branch/container invariant** — natural isolation, parallelism, clean teardown, one PR per task.
- **Agent-as-untrusted-code** posture and the trust ordering (user device > VM > container > internet).
- **PR-as-approval-gate** — no platform-side review UI needed in V1.
- **Repo-scoped fine-grained tokens** — bounded blast radius.

### 1.3 How the approved decisions strengthen the original model

The two decisions that moved furthest from the first draft, and why:

- **D3 + D4 (host-only token, full transfer model).** The untrusted container now shares **no git repository and no token** with the host. It receives a self-contained copy of the source, produces local commits, and emits an inert `git bundle`. The host is the sole writer to the real repo and the sole holder of the token. This closes the `.git`-hook escape class entirely: there is no agent-writable `.git` that a host git command ever reads or executes. The only secret that must exist inside the container is the **model API key** (the agent has to call the model) — rotatable, scoped, not infrastructure.
- **D5 (rootless Podman; gVisor deferred).** Rootless Podman removes the root daemon, so a break-out lands unprivileged rather than as host root — the property that matters on a box sitting next to the home LAN. gVisor would add a userspace kernel so the agent's syscalls never hit the host kernel directly; that layer is deferred to a validation spike (#17) because the runner is swappable and the Devbox VM is already a hypervisor guest. The first-shipped protection against reaching the home network is the **egress deny-list** (R2/§8.5), which is independent of the runtime.

### 1.4 Residual risks (tracked)

| # | Risk | Mitigation | Where |
|---|------|------------|-------|
| R1 | gVisor + rootless-Podman networking has known rough edges | gVisor deferred (D5 amendment); validated in its own spike (#17) before adoption — runner is abstracted so the runtime is swappable. The spine ships on rootless Podman without it | §8.1, #17 |
| R2 | Network egress is the highest-severity control | Isolated network + **deny private ranges** (not allow-list); tested as an M2 gate | §8.5 |
| R3 | Daemon holds the Docker/Podman control socket + tokens = crown jewel | Unix-socket only, no TCP; OS access = Tailscale+SSH; append-only audit log | §5, §10.2 |
| R4 | Per-task source copy costs I/O vs a shared object store | Acceptable on 200 GB SSD for known repos; shallow/single-branch copy; revisit for large monorepos | §8.4 |
| R5 | Agent runaway (loops, cost) | 30-min wall-clock timeout → `Failed`; resource caps | §7.4, §8.6 |

---

## 2. Resolved requirement gaps

These were flagged as missing in the first draft and are now resolved by the decisions above:

- Repo identity mapping → **D11** static registry.
- Branch naming → **[DEFAULT]** `agent/<agent>/<…>`.
- Prompt construction → host fetches issue / uses `--task`, renders a prompt copied in read-only (§7.2, §9.3).
- Non-issue tasks → **D8** `--task`.
- Base image scope → **D6**.
- Timeout / runaway → **D10** 30-min default.
- Concurrency → **D10** 2 concurrent.
- Secret storage at rest → **[DEFAULT]** systemd `LoadCredential`.
- Success definition → **D9**.
- PR metadata → **[DEFAULT]** templated body.

Still genuinely open: see §3.

---

## 3. Open questions (non-blocking, tracked)

- **[OPEN] OQ-1 — rootless network mode + egress (now) / gVisor interop (deferred).** The rootless network mode (pasta/slirp4netns) and the egress nft rules are settled by the #4 spike. The `runsc` half — whether gVisor interoperates cleanly with the chosen network mode + egress rules — is deferred to the gVisor spike (#17, per the D5 amendment).
- **[OPEN] OQ-2 — Base-image rebuild cadence.** How/when toolchains and agent CLIs get updated (manual rebuild vs scheduled). Deferred; manual for V1.
- **[OPEN] OQ-3 — Multiple GitHub orgs.** V1 assumes one machine user; confirm no second org is needed before M4.
- **[OPEN] OQ-4 — Large-repo performance** of the per-task source copy (R4). Measure on a real repo in M3; optimize only if needed.

---

## 4. Repository structure

Single Go module, single binary `agent-task` (with a `serve` subcommand for the daemon). Internal packages map to the components in 0002.

```
devbox/
├── cmd/
│   └── agent-task/            # CLI + `serve` (daemon) entrypoint
├── internal/
│   ├── config/                # config.yaml load/validate; repo registry; secret refs
│   ├── controller/            # task lifecycle, state machine, worker pool, cancellation
│   ├── repo/                  # host-side: mirror cache, fetch/sync, feature-branch worktree,
│   │                          #   build source-export for the container, apply bundle back
│   ├── runner/                # Podman+gVisor: container create/start/stop/destroy, isolated
│   │                          #   network + egress policy, resource limits, source copy-in,
│   │                          #   bundle copy-out
│   ├── agent/                 # adapters behind one interface: claude (M3), codex (M6); pi later
│   ├── github/                # issue fetch, host push, PR create (token used ONLY here)
│   ├── store/                 # SQLite: tasks, events, artifacts, repos
│   ├── prompt/                # deterministic prompt rendering (issue JSON or --task text)
│   └── obs/                   # structured logging (slog), artifact writers, audit events
├── images/
│   ├── base/                  # Dockerfile: Node/TS+Bun, Go, Python3 + essentials + agent CLIs
│   └── README.md              # how the base image is built/updated (OQ-2)
├── deploy/
│   └── systemd/               # agent-taskd.service + LoadCredential wiring
├── config.example.yaml        # documented example (repo registry, limits) — NO real secrets
├── docs/
│   ├── project/               # vision / architecture / security (source requirements)
│   ├── agents/                # issue-tracker / triage-labels / domain (skill config)
│   └── adr/                   # ADRs, created as decisions are revisited
└── TECHNICAL_DESIGN.md        # this document
```

---

## 5. Runtime architecture

```
 ┌─────────────┐  Tailscale   ┌──────────────────────── Devbox VM ─────────────────────────┐
 │ User device │ ───SSH────►  │  agent-task (CLI) ──unix socket──► agent-taskd (daemon)      │
 └─────────────┘              │                                       │                       │
                              │                       ┌───────────────┴───────────────┐       │
                              │                       │ controller: worker pool        │       │
                              │                       │ (max 2 concurrent), state mgmt │       │
                              │                       └──┬───────────┬─────────┬────────┘       │
                              │     store (SQLite) ◄─────┘           │         │                │
                              │     repo mirror + host feature       │         │                │
                              │       worktree (host-only git) ◄─────┤   github (token here)    │
                              │                                       ▼                          │
                              │                            runner ► Podman+gVisor ► [container]  │
                              │      copy source IN ─────────────────►│  (no token, no shared    │
                              │      bundle OUT ◄─────────────────────│   repo, isolated net)    │
                              └───────────────────────────────────────┼──────────────────────────┘
                                                                       ▼
                                                       Internet: model APIs, registries
                                                       (private ranges DENIED → no home LAN)
```

- **`agent-task` (CLI):** `run`, `status`, `logs [-f]`, `cancel`, `ls`, `repos`. Thin client over `/run/agent-task.sock` (`0660`, owner = service user). No TCP surface (D2) → OS access (already gated by Tailscale+SSH) is the auth boundary.
- **`agent-taskd` (daemon, `agent-task serve`):** owns the SQLite DB, the repo mirror cache, the host-side feature worktrees, the Podman socket, and the GitHub token. Bounded worker pool: **2** concurrent tasks (D10). Each task = one goroutine walking the lifecycle (§7) with a `context` for cancellation/timeout. Survives client disconnect (long tasks keep running — 0001's pain point).

---

## 6. Data model

SQLite (`data/agent-task.db`), daemon-only access. Plain versioned SQL migrations.

```sql
repos(                            -- seeded from config.yaml registry (D11)
  id            INTEGER PK,
  name          TEXT UNIQUE,      -- "case-tracker-fc"
  github_owner  TEXT,
  github_repo   TEXT,
  default_branch TEXT,
  mirror_path   TEXT,             -- host mirror clone
  token_ref     TEXT              -- name of the LoadCredential secret, NOT the token itself
)

tasks(
  id            TEXT PK,          -- short ulid-style id
  repo_id       INTEGER FK,
  source        TEXT,             -- 'issue' | 'task'  (D8)
  issue_number  INTEGER NULL,
  prompt_path   TEXT,             -- rendered prompt artifact (host)
  agent         TEXT,             -- 'claude' | 'codex'
  branch        TEXT,             -- agent/<agent>/<slug>-<shortid>
  host_worktree TEXT,             -- host-side worktree where the bundle is applied
  container_id  TEXT NULL,
  state         TEXT,             -- Created|Running|Completed|Failed|Cancelled (0002 verbatim)
  commit_count  INTEGER NULL,     -- drives D9 success check
  exit_code     INTEGER NULL,
  pr_url        TEXT NULL,
  summary       TEXT NULL,
  error         TEXT NULL,
  created_at    TIMESTAMP,
  started_at    TIMESTAMP NULL,
  finished_at   TIMESTAMP NULL
)

task_events(                      -- append-only audit trail (security log)
  id INTEGER PK, task_id TEXT FK, ts TIMESTAMP,
  type TEXT,                      -- 'state_change' | 'phase' | 'warning' | 'action'
  message TEXT
)

artifacts(                        -- pointers to on-disk files under data/tasks/<id>/
  id INTEGER PK, task_id TEXT FK,
  kind TEXT,                      -- 'agent_log'|'transcript'|'test_output'|'bundle'|'diff'|'summary'
  path TEXT
)
```

Note `token_ref` stores a *credential name*, never the secret. Bundles/logs live on disk under `data/tasks/<id>/`; only metadata is in the DB.

---

## 7. Task lifecycle

### 7.1 States (verbatim from 0002 — not extended)

```
Created ──► Running ──► Completed
   │           ├──────► Failed
   │           └──────► Cancelled      (user cancel or timeout while Running)
   └──► Cancelled                      (cancel before start)
```

Internal *phases* below occur **within `Running`** and are recorded as `task_events(type='phase')`, preserving the documented 3-state machine.

### 7.2 Phases within `Running` (transfer model)

1. **Sync repo** — ensure host mirror exists; `git fetch` the default branch.
2. **Create feature branch + host worktree** — host creates `agent/<agent>/<slug>-<shortid>` off the default branch in a host-controlled worktree (hooks disabled: `core.hooksPath=/dev/null`, `GIT_CONFIG_NOSYSTEM`).
3. **Render prompt** — fetch the issue via `gh` (D8 `--issue`) or use `--task` text; write the prompt artifact.
4. **Build source export** — host produces a self-contained copy of the repo at the base commit (shallow/single-branch) to hand to the container. **No remote, no token, no host `.git` shared.**
5. **Launch container** — runner starts the rootless-Podman container (§8; gVisor runtime once #17 lands): source copied in, prompt read-only, artifact drop-dir for output, isolated network, resource caps, model API key via env.
6. **Execute agent** — adapter runs the agent against the in-container copy; it makes local commits; stdout/transcript stream to artifacts.
7. **Extract** — host obtains a `git bundle` of the agent's commits (`base..HEAD`) from the container's artifact drop-dir (inert data; never an agent-writable `.git` the host executes).
8. **Apply host-side** — host `git fetch`/cherry-picks the bundle onto the feature branch in its hook-disabled worktree; captures the resulting `diff`; counts commits (`commit_count`).
9. **Publish** — host pushes the branch with the repo-scoped token and opens the templated PR (§9); back-links the issue.
10. **Teardown** — destroy the container and its ephemeral storage; keep the host feature branch/worktree for inspection (pruned later).

### 7.3 Terminal outcomes (D9)

- **Completed** — agent exited `0` **and** `commit_count ≥ 1`. Tests are run, captured, and attached to the PR, but a failing test does **not** fail the task (PR review is the gate).
- **Failed** — non-zero exit, timeout, zero commits, or an internal error (push/apply failure). Reason recorded in `error`.
- **Cancelled** — user cancel; container stopped, no PR, branch/worktree left for inspection.

### 7.4 Cancellation & timeout

- `agent-task cancel <id>` → daemon cancels the task `context` → container SIGTERM→SIGKILL → `Cancelled` → teardown.
- 30-min wall-clock timeout (D10) fires the same cancel path with `error="timeout"` → `Failed`.

---

## 8. Container execution model

Every choice assumes the agent inside is hostile (0003).

### 8.1 Runtime (D5)
**Rootless Podman** as the engine, shipping with the standard OCI runtime; **gVisor (`runsc`)** is the intended hardening layer but is *validated-deferred* (D5 amendment — spike #17). The `runner` package wraps the engine behind an interface so the runtime is swappable (so `runsc` can flip on later, plus a Docker fallback or future Kata) without touching the controller. The rootless network mode (pasta/slirp4netns) + egress rules is validated by the **#4 spike** (R2); `runsc` interop with that network mode is validated by **#17** (R1).

### 8.2 No shared git repository (D4 — the core isolation property)
The container is handed a **copy** of the source, not a mount of the real repo. There is no host-writable `.git` that any host process later reads/executes → the `.git`-hook escape class does not exist here.

### 8.3 What crosses the boundary (and nothing else)
- **In:** the source export (copied into container-local writable storage), and `/task/prompt.md` **read-only**.
- **Out:** a single **artifact drop-dir** (`/task/out`, a fresh empty per-task host dir) where the agent writes the `git bundle`, transcript, summary, and test output. The host reads these as **inert data** — it never executes anything from it. (Equivalent extraction via `podman cp` is acceptable; the drop-dir is the [DEFAULT].)
- **Never:** the GitHub token, the host `/`, `$HOME`, the SSH agent, the Podman socket, the real repo or its `.git`.

### 8.4 Source delivery
Host builds a shallow/single-branch copy at the base commit and copies it into the container's own writable layer/volume (R4 cost accepted for known repos; revisit for large monorepos — OQ-4).

### 8.5 Network egress — the critical control (R2)
- Container on a **dedicated isolated network**, never host networking, **never the tailnet** (containers must not reach the home LAN via Tailscale — 0003).
- **Policy = allow general internet, DENY private/internal ranges:** RFC1918 (`10/8`, `172.16/12`, `192.168/16`), `100.64/10` (Tailscale CGNAT), link-local, plus any configured internal subnets (Proxmox/TrueNAS/Jellyfin). This directly enforces "containers must not bridge into the home network" and is far more practical than allow-listing every registry/CDN/model-provider IP. Enforced via nft/iptables on the isolated network.
- DNS restricted to a public resolver (no internal name resolution).
- **M2 gate:** a test proves the container *can* reach the internet and *cannot* reach a known LAN host or the tailnet.

### 8.6 Resource limits (D10, R5)
`--cpus`, `--memory`, `--pids-limit`, and a tmpfs/disk quota, sized so 2 concurrent tasks fit comfortably in 16 GB. 30-min wall-clock timeout per task.

### 8.7 Process hardening & image (D6)
- Non-root user; `--cap-drop ALL` (re-add only if required); `--security-opt no-new-privileges`; read-only root fs except the source copy, `/task/out`, and a tmpfs `/tmp`.
- **Base image** (`images/base`): Node LTS + **Bun**, Go, Python 3, plus `git`, `ripgrep`/`fd`, build toolchain, `curl`, and the agent CLIs (Claude Code, Codex). Build/update process documented (OQ-2).

### 8.8 Agent adapter interface (D7)
One Go interface; each agent maps: image/command, how the prompt is passed, how to detect completion + exit code, where the transcript/summary land in `/task/out`. **Claude Code** lands in M3; **Codex** in M6 (proves the abstraction). **Pi** is designed-for (interface accommodates it) but not built.

### 8.9 Disposability
Container destroyed on completion/failure/cancel/daemon-restart-recovery; never reused. Startup orphan sweep removes containers/host-worktrees for tasks already in a terminal state.

---

## 9. GitHub integration strategy

### 9.1 Token boundary (D3)
The repo-scoped fine-grained token is used **only** in the `github` package, **only on the host**, and is **never** placed in the container, the source export, the bundle, or the DB (only a `token_ref` name is stored). A compromised agent has no token to steal.

### 9.2 Credentials ([DEFAULT])
One dedicated **machine user**; **fine-grained, repository-scoped** tokens, one per registered repo (D11), loaded via systemd `LoadCredential`. No personal/org-admin tokens (enforced by config policy + operator runbook).

### 9.3 Flow
1. **Issue fetch** (`--issue`): `gh issue view N --json …` on the host → rendered into the prompt artifact. (`--task`: skip; use the provided text.)
2. **Commits**: produced by the agent inside the container, extracted as a bundle, applied host-side onto `agent/<…>` (§7.2).
3. **Push**: host pushes the feature branch with the repo-scoped token.
4. **PR**: host opens a PR (base = default branch) with the templated body (task id, agent, issue link, summary, test results, "🤖 agent-produced — review required"). **Never auto-merged.**
5. **Back-link**: store `pr_url`; optionally comment the PR link on the source issue.

### 9.4 Future (vision, not V1)
The `agent/<agent>/…` branch namespace is reserved so GitHub **branch-protection / rulesets** can later govern agent branches server-side (e.g. block non-`agent/*` pushes by the machine user, require PR for protected branches). Designed-for; not built in V1.

---

## 10. Security considerations

Mapped to 0003.

### 10.1 Untrusted container (Security Philosophy)
§8: rootless Podman, non-root, cap-drop, read-only rootfs, no host mounts beyond an inert drop-dir, no shared repo/token, resource caps, disposability. gVisor (deferred — D5 amendment, #17) would additionally interpose a userspace kernel so the boundary is not the host kernel alone; until it lands, the kernel boundary is the host kernel plus the Proxmox-guest VM layer below it.

### 10.2 Trust boundaries & the daemon (Trust Boundaries, R3)
Daemon exposes only a Unix socket (no TCP); access requires an OS session (already gated by Tailscale+SSH). The daemon is the crown jewel (Podman socket + tokens) — keeping it off the network is the primary control. `task_events` is an append-only audit trail.

### 10.3 Secrets (Secrets, [DEFAULT])
Allowed: dev creds, repo creds, model API keys. Prohibited: production/cloud-admin creds, personal SSH keys — enforced by never wiring them in, plus an operator runbook. At rest: root-owned `0600`, via systemd `LoadCredential`; model key passed to the container by env at launch, never baked into the image. The GitHub token never enters the container at all (D3).

### 10.4 Network (Network Restrictions / Tailscale Model, R2)
§8.5 egress deny-list. **Invariant: the container is never on the tailnet.** Tailscale serves only user→VM; it is not shared into containers — the concrete mechanism for "no bridge into the home network."

### 10.5 Approval gates
The only outbound write to GitHub is *push branch + open PR*. No merge/deploy/infra/secret changes by the platform. Everything else is human, via PR review.

### 10.6 Compromise success criteria
A compromised container has: no host fs, no tailnet, no private-range egress, no GitHub token, no shared repo, no infra creds, and (once #17 lands) a gVisor-mediated kernel boundary → cannot reach Proxmox/NAS/workstation or production secrets. Until gVisor lands, the breakout still lands as an unprivileged user inside the Proxmox-guest VM, contained by the egress deny-list. Worst realistic case: abuse of the rotatable model API key and mischief inside its own throwaway source copy (discarded). Meets the stated bar.

---

## 11. Logging & observability

- **Structured logs** (Go `slog`, JSON) → systemd journal.
- **Per-task artifacts** under `data/tasks/<id>/`: `agent.log`, `transcript.*`, `test_output.txt`, `changes.bundle`, `diff.patch`, `summary.md`; indexed in `artifacts`.
- **Audit trail:** `task_events` records every state change + security-relevant action (container launched/destroyed, bundle extracted, push, PR opened, cancel).
- **CLI:** `agent-task logs <id> [-f]` (stream from daemon), `status <id>` (state + phase + PR), `ls` (recent tasks).
- **Out of scope for V1 (named, not built):** metrics/Prometheus, tracing, dashboards, Discord notifications. `obs` leaves the seams.

---

## 12. V1 implementation milestones

Each milestone is independently reviewable, ends in a working vertical slice, and ships with its own tests. No milestone merges with failing tests.

- **M0 — Skeleton & store.** Go module; `agent-task`/`serve` scaffolding; `config.yaml` loader + repo registry (D11); SQLite store + migrations; task/event/artifact models. *Demo:* `agent-task repos` lists registered repos; `ls` shows an empty table. *Tests:* config validation, store CRUD, migration.
- **M1 — Host repo & worktree manager.** Mirror clone/cache; fetch/sync; feature-branch worktree with hooks disabled; branch naming; orphan sweep. *Demo:* create + tear down a feature branch/worktree for a real repo. *Tests:* worktree create/remove, hook-disable, cleanup idempotency.
- **M2 — Runner + isolation (security spine).** Rootless Podman spike (#4); container with source copy-in, drop-dir copy-out, non-root/cap-drop/read-only rootfs, resource limits, isolated network + **egress deny-list** (R2). gVisor (`runsc`) is validated-deferred — its spike (#17, R1) runs in parallel and feeds adoption, but the spine does not block on it. *Demo (key gate):* container reaches the internet but **cannot** reach a known LAN host or the tailnet; bundle round-trips out. *Tests:* mount/boundary scoping, limits, egress assertions.
- **M3 — Claude Code adapter (end-to-end agent run).** Adapter interface + Claude adapter; prompt rendering (D8); run agent against the in-container copy; extract bundle; apply onto the feature branch host-side; capture log/transcript/diff/summary; measure copy cost (OQ-4). *Demo:* agent makes a real change in a test repo; commits land on the host feature branch; token never in container. *Tests:* adapter contract, bundle apply, success/timeout paths.
- **M4 — GitHub integration (host-mediated).** Issue fetch → prompt; host push; templated PR; machine-user repo-scoped token via `LoadCredential` (D3). *Demo:* `agent-task run --repo case-tracker-fc --issue 34 --agent claude` → open PR + logs + summary + test results. *Tests:* prompt-from-issue rendering, PR body templating, token-isolation assertion.
- **M5 — Lifecycle, concurrency & control.** Full state machine (§7); worker pool + 2-concurrent cap (D10); `cancel`; 30-min timeout; restart recovery + orphan teardown; audit events. *Demo:* two concurrent tasks; cancel one; restart daemon → clean recovery. *Tests:* transitions, cancellation, concurrency cap, recovery.
- **M6 — Codex adapter + hardening.** Codex adapter (proves the abstraction; Pi deferred — D7); security-review pass; operator runbook; `config.example.yaml`; ADRs for D1–D11. *Demo:* same task via `--agent codex`. *Tests:* adapter parity suite.

The 0001 success criterion (`agent-task run --repo case-tracker-fc --issue 34 --agent claude` → branch + PR + logs + summary + test results) is met at **M4** and hardened through **M6**.

---

## Decision log

| ID | Decision | Choice | Status |
|----|----------|--------|--------|
| D1 | Language | Go | Approved |
| D2 | Process architecture | Daemon + CLI, Unix socket | Approved |
| D3 | Token location | Host-only | Approved |
| D4 | Commit transfer | Full transfer model (copy in / bundle out) | Approved |
| D5 | Runtime | Rootless Podman (ships); gVisor deferred-validated (#17) | Approved — amended 2026-06-23 (#4) |
| D6 | Base image | Batteries-included: Node/TS+Bun, Go, Python 3 | Approved |
| D7 | Agents | Claude Code + Codex; Pi deferred | Approved |
| D8 | Task input | Issue or free-form | Approved |
| D9 | Success def | Exit 0 + ≥1 commit; tests informational | Approved |
| D10 | Limits | 2 concurrent / 30-min timeout | Approved |
| D11 | Repo resolution | Static allowlist in config | Approved |

ADRs for D1–D11 to be written in `docs/adr/` during M6 (or earlier if a decision is revisited).

---

*End of design baseline. Implementation begins on approval, milestone by milestone (M0 first).*
