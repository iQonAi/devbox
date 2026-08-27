# 0001 — Agent-Task VM Operator Runbook

Operator (HITL) procedures for the Agent-Task VM that hosts the agent orchestrator
and execution containers. This VM is provisioned and maintained **by hand**: the
agent platform must never have Proxmox access (`docs/project/0003`). Proxmox is
human-only.

Related: issue #2 (provisioning), issue #3 (Tailscale + SSH),
`docs/project/0002` (architecture / sizing),
`docs/project/0003` (security & threat model).

## Host & sizing

| Property   | Value                       |
| ---------- | --------------------------- |
| Hypervisor | Proxmox (human-only access) |
| vCPU       | 8                           |
| RAM        | 16 GB                       |
| Disk       | 250 GB SSD                  |

`docs/project/0002` lists 200 GB SSD as the _recommended_ minimum; this VM was
provisioned with 250 GB, which exceeds the recommendation.

## Operating system

- **Ubuntu Server, minimal install.** Chosen as a current LTS-class Linux server
  OS suitable for rootless Podman + gVisor with unprivileged user namespaces
  (the isolation model in `docs/project/0002`).
- Exact release: **Ubuntu 22.04.5 LTS** (`lsb_release -ds`).
- Post-install: `sudo apt-get update && sudo apt-get upgrade`, then installed
  baseline tooling (`curl`, `jq`, `wget`, `qemu-guest-agent`, ...).

## Provisioning checklist (issue #2)

All items confirmed on the VM as the operator user (verified 2026-06-23).
Re-run the commands to re-verify after any rebuild. Host-specific addresses are
redacted here and recorded out-of-band.

1. **Non-root operator user + unprivileged user namespaces** — confirmed

   ```bash
   id                                            # uid=1000(operator), member of sudo
   sysctl kernel.unprivileged_userns_clone       # = 1
   grep "^$(whoami):" /etc/subuid /etc/subgid    # subuid/subgid range 100000:65536 present
   ```

   Note: `kernel.apparmor_restrict_unprivileged_userns` does **not** exist on
   22.04 (introduced in 24.04), so the AppArmor userns restriction that affects
   rootless Podman on newer Ubuntu is not present on this VM.

2. **Reachable on LAN** (for later Tailscale / SSH setup) — confirmed

   ```bash
   ip -4 addr show scope global   # enp6s18, 192.168.1.<redacted>/24 (dynamic DHCP lease)
   systemctl is-enabled ssh       # enabled
   ```

   The LAN address is a dynamic DHCP lease; reserve it (DHCP reservation or
   static) before relying on it for SSH/Tailscale.

3. **Outbound internet** — confirmed

   ```bash
   curl -fsSI https://github.com >/dev/null && echo "outbound OK"   # outbound OK
   ```

4. **This runbook** records sizing and OS choice (above) — done.

## Tailscale + SSH access (issue #3)

Confirmed on the VM (verified 2026-06-23). Tailnet address redacted; recorded
out-of-band. Security invariant (`docs/project/0003`): Tailscale serves
**user→VM only** and must never be shared into execution containers.

### Tailscale (user → VM only)

- Installed via `curl -fsSL https://tailscale.com/install.sh | sh`; joined with
  `sudo tailscale up` — **no** `--advertise-routes`, **no** `--advertise-exit-node`.
- Tailnet IP: `100.x.y.<redacted>` (host `agent-task`).
- Host-only scoping confirmed: not an exit node, no subnet routes advertised,
  `ExitNodeAllowLANAccess=false` (`tailscale debug prefs`). Tailscale does not
  bridge the home LAN into the tailnet.
- **Invariant for later milestones:** containers must get their own isolated
  egress (Internet only) and must never see `tailscale0`. Do not advertise
  routes or run Tailscale inside a container.

### SSH (hardened, key-only)

- sshd listens on all interfaces (`0.0.0.0:22`, `[::]:22`) — reachable over both
  tailnet and LAN. ufw inactive.
- Hardening drop-in `/etc/ssh/sshd_config.d/10-agent-task-hardening.conf` (sorts
  before the cloud-image default `50-cloud-init.conf`, so it wins on first-match):

  ```
  PermitRootLogin no
  PasswordAuthentication no
  PubkeyAuthentication yes
  KbdInteractiveAuthentication no
  ```

  ```bash
  sudo sshd -t && sudo systemctl restart ssh
  sudo sshd -T | grep -Ei 'passwordauthentication|permitrootlogin|kbdinteractive'
  # -> passwordauthentication no / permitrootlogin no / kbdinteractiveauthentication no
  ```

- Verified: key login from an authorized device succeeds; a device without an
  authorized key is rejected (`Permission denied (publickey)`).
- **Gotchas:** the Ubuntu cloud image ships `50-cloud-init.conf` with
  `PasswordAuthentication yes` — the `10-` drop-in overrides it by lexical order.
  Disabling `PasswordAuthentication` alone is insufficient on Ubuntu when PAM
  keyboard-interactive is enabled, so `KbdInteractiveAuthentication no` is also set.

### Reaching `agent-task` over the tailnet

The orchestrator daemon listens on a **Unix socket, not TCP** (`CLAUDE.md`,
decision D2). Access path: SSH to the VM over Tailscale → run the `agent-task`
CLI locally → CLI talks to the daemon over the Unix socket. No TCP port is
exposed on the tailnet. To be validated end-to-end once the daemon exists (M0).

## Rootless Podman + egress deny-list (issue #4)

Confirmed on the VM (verified 2026-07-16). This is the isolation **spine** for
M2/R2 and closes the rootless half of OQ-1. Scope amended per D5: gVisor
(`runsc`) is deferred to issue #17 and is **not** installed here.

### Engine & network mode (OQ-1 resolved: slirp4netns)

- **Podman 3.4.4** (Ubuntu 22.04 distro package). This is the pre-netavark era:
  **no netavark, no pasta** available. Rootless networking backend is
  **`slirp4netns`** — that is the chosen mode for OQ-1, dictated by the distro.
- Rootless prerequisites already confirmed in the provisioning checklist
  (`unprivileged_userns_clone=1`, subuid/subgid ranges present, no 24.04-style
  AppArmor userns restriction).
- **`nftables` is absent; `iptables` is present** → enforcement uses `iptables`,
  which avoids a package add (dependency-vetting rule).

### Mechanism — why filter host-side by uid

`slirp4netns` does userspace NAT: it opens the container's egress sockets **from
the host network namespace, as the uid running Podman**. So the host firewall
never sees per-container source IPs (unlike rootful), but it _can_ match the
**owning uid**. Enforcement therefore:

1. Runs all agent containers as a **dedicated unprivileged user `agentbox`
   (uid 999)** — never the operator. This keeps the deny rules off the
   operator's own SSH / LAN / Tailscale traffic (lockout avoidance).
2. Attaches an `iptables OUTPUT -m owner --uid-owner <agentbox>` REJECT for each
   denied range.

### Denied ranges

RFC1918 + Tailscale CGNAT + link-local — covers LAN, tailnet, and cloud-metadata
in one list (internal service subnets Proxmox/TrueNAS/Jellyfin all sit inside
RFC1918, so they are denied by `192.168.0.0/16` / `10.0.0.0/8`):

```
10.0.0.0/8        # RFC1918
172.16.0.0/12     # RFC1918
192.168.0.0/16    # RFC1918 (covers the home LAN + its gateway)
100.64.0.0/10     # Tailscale CGNAT (tailnet)
169.254.0.0/16    # link-local + cloud metadata 169.254.169.254
```

**DNS** is pinned to a public resolver at run time (`--dns 9.9.9.9`); internal
resolvers are auto-denied because they live inside RFC1918.

**Caveat — IPv4 only.** These rules are `iptables`, not `ip6tables`.
`slirp4netns` defaults to no container IPv6 here, so that is sufficient today. If
IPv6 egress is ever enabled, add the `ip6tables` twin (`fe80::/10`, `fc00::/7`).

### Persistence (survives reboot)

Rules are applied at boot by a systemd oneshot running an idempotent script (no
package add; reversible via `systemctl disable --now agentbox-egress.service`).

- `/usr/local/sbin/agentbox-egress.sh` — resolves `agentbox`'s uid, then for
  each denied net deletes any existing matching rule and re-adds it once
  (flush-then-readd = idempotent, no duplicates on re-run).
- `/etc/systemd/system/agentbox-egress.service` — `Type=oneshot`,
  `RemainAfterExit=yes`, `After/Wants=network-online.target`, enabled on
  `multi-user.target`.

```bash
sudo systemctl enable --now agentbox-egress.service
sudo systemctl --no-pager status agentbox-egress.service   # Active: active (exited), ExecStart status=0
sudo iptables -L OUTPUT -n | grep -c 'owner UID match 999' # -> 5
```

### Exit-code capture (containers.conf)

Rootless Podman on 22.04 threw `failed to get journal cursor` when reading a
container's exit code. Fixed by setting `events_logger="file"` in
`/home/agentbox/.config/containers/containers.conf`. This matters because the
runner keys success on the container's exit code (design D9).

### Validation (all confirmed)

Run pattern — from a cwd `agentbox` can read (e.g. `/tmp`), as `agentbox`:

```bash
cd /tmp && sudo -u agentbox env HOME=/home/agentbox XDG_RUNTIME_DIR=/run/user/999 \
  podman run --rm --dns 9.9.9.9 alpine sh -c '...'
```

| Target                        | Expected  | Result       |
| ----------------------------- | --------- | ------------ |
| Public DNS (`quad9.net`)      | resolves  | `dns-OK`     |
| Public HTTP (`icanhazip.com`) | reachable | `public-OK`  |
| `10.0.0.1` (RFC1918)          | blocked   | `blocked-OK` |
| `172.16.0.1` (RFC1918)        | blocked   | `blocked-OK` |
| home-LAN gateway (`<lan-gw>`) | blocked   | `blocked-OK` |
| tailnet host (`100.64/10`)    | blocked   | `blocked-OK` |
| `169.254.169.254` (metadata)  | blocked   | `blocked-OK` |

Baseline (before the deny-list) confirmed the hole was real: a default rootless
container **did** reach the LAN gateway and the tailnet host. The rules close it.

### Invariant for later milestones

This host-global uid-deny is the **spike's** enforcement. When the runner is
built, per-container isolated egress becomes the daemon's job; it must preserve
the same deny-list. `agentbox` (or its successor runner user) must never be
granted a path around these rules, and containers must never see `tailscale0`.

## Host toolchain + agent-taskd service (issue #5)

Confirmed on the VM (verified 2026-07-16). Prepares the host to build and run
the orchestrator. The `agent-task` binary does **not** exist yet (M0 unstarted),
so the systemd unit is installed but **not started** — this is a skeleton.

### Toolchain

| Tool | Version    | Source                                                                                                      |
| ---- | ---------- | ----------------------------------------------------------------------------------------------------------- |
| Go   | **1.26.5** | Official pinned tarball → `/usr/local/go` (see below)                                                       |
| git  | 2.34.1     | distro (`apt`)                                                                                              |
| gh   | 2.4.0      | distro (`apt`) — old (2022); upgrading via the official GitHub CLI apt repo is recommended but not required |

The distro Go is 1.18 (too old). Install the current stable Go from `go.dev`,
checksum-verified, into `/usr/local/go`; a `PATH` drop-in makes new shells prefer
it over the apt `go1.18` at `/usr/bin/go` (left in place, shadowed):

```bash
GOVER=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)
cd /tmp && curl -fsSLO "https://go.dev/dl/${GOVER}.linux-amd64.tar.gz"
EXPECT=$(curl -fsSL 'https://go.dev/dl/?mode=json&include=all' \
  | jq -r --arg f "${GOVER}.linux-amd64.tar.gz" '.[].files[]|select(.filename==$f)|.sha256')
echo "${EXPECT}  ${GOVER}.linux-amd64.tar.gz" | sha256sum -c -    # must print OK
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf "${GOVER}.linux-amd64.tar.gz"
echo 'export PATH=/usr/local/go/bin:$PATH' | sudo tee /etc/profile.d/go.sh >/dev/null
```

### Repo clone

Cloned to **`/opt/agent-task`** (owned by `qdrtech`) for building. The repo is
private, so it is cloned with the **operator's own `gh` identity** (`gh auth
login`, device flow) — deliberately **separate** from the daemon's machine-user
GitHub token (D3), which is never used for source and is wired only at M4.

```bash
sudo mkdir -p /opt/agent-task && sudo chown "$USER:$USER" /opt/agent-task
gh auth status || gh auth login
gh repo clone iQonAi/agent-task /opt/agent-task
```

### Service user (`agent-taskd`, distinct from `agentbox`)

The daemon runs as a dedicated system user **`agent-taskd`** (uid 998, own
group, `/usr/sbin/nologin`, home `/var/lib/agent-task`) — **not** `agentbox`.
Rationale: `agentbox` (uid 999) is the untrusted-container runner and the
deliberate blast-radius target; keeping the token/DB owner a _different_ uid
means a container escape reaching host uid 999 still cannot read the daemon's
secrets. The operator (`qdrtech`) is added to the `agent-taskd` group so the CLI
can reach the daemon socket (needs a re-login to take effect).

```bash
sudo useradd --system --user-group --home-dir /var/lib/agent-task --no-create-home \
  --shell /usr/sbin/nologin --comment "agent-taskd daemon" agent-taskd
sudo usermod -aG agent-taskd qdrtech
```

### systemd unit + LoadCredential

Unit source of truth: **`deploy/systemd/agent-taskd.service`** in the repo;
install a copy to `/etc/systemd/system/`. Key settings: `User/Group=agent-taskd`,
`Type=notify`, `RuntimeDirectory=agent-task` (socket
`/run/agent-task/agent-task.sock`, `0660`), `StateDirectory=agent-task` (DB in
`/var/lib/agent-task`), `Restart=on-failure`, and `LoadCredential=` for secrets.
`ExecStart=/usr/local/bin/agent-task serve` — the binary is produced at M0, so
**do not `systemctl start`** until then; only `daemon-reload`.

Aggressive sandboxing (`NoNewPrivileges`, `ProtectSystem=strict`, syscall
filters) is intentionally left out for now: the M2 runner's cross-user Podman
hop (daemon → `agentbox`) will dictate what is compatible.

Secrets are root-owned `0600` source files under `/etc/agent-task/credentials/`
(dir `0700 root:root`), delivered by `LoadCredential` into the service's private
`$CREDENTIALS_DIRECTORY` (`0400`, owned by `agent-taskd`, unreadable by anyone
else). Per-repo, repository-scoped GitHub tokens (D11) are added one line each at
M4; a sentinel placeholder stands in until then.

```bash
sudo install -d -m 0700 -o root -g root /etc/agent-task/credentials
# real repo-scoped token replaces this sentinel at M4:
printf 'SENTINEL-...' | sudo tee /etc/agent-task/credentials/github-token >/dev/null
sudo chmod 0600 /etc/agent-task/credentials/github-token
sudo systemctl daemon-reload   # NOT start — binary is M0
```

### Validation (all confirmed)

- **Static:** `systemd-analyze verify /etc/systemd/system/agent-taskd.service`
  reports only `Command /usr/local/bin/agent-task ... No such file` — expected,
  the binary is M0; the unit itself parses clean.
- **Secret negative:** `sudo -u agent-taskd cat /etc/agent-task/credentials/github-token`
  → `Permission denied` (the `0600 root:root` source is unreadable by the service user).
- **Secret positive (only via LoadCredential):**

  ```bash
  sudo systemd-run --uid=agent-taskd --pipe --wait -q \
    -p LoadCredential=github-token:/etc/agent-task/credentials/github-token \
    /bin/sh -c 'cat "$CREDENTIALS_DIRECTORY/github-token"; ls -l "$CREDENTIALS_DIRECTORY"'
  ```

  delivers the secret as `-r-------- agent-taskd` in `$CREDENTIALS_DIRECTORY`.
  Together with the negative test this proves the service user reaches secrets
  **only** through `LoadCredential`.

### Deferred (not this issue)

- ~~**M0:** build the binary, implement sd_notify readiness, then
  `systemctl enable --now`.~~ Done — see "M0 orchestrator deploy (issue #8)".
- **M2:** cross-user hop for the daemon to drive `agentbox`'s rootless Podman;
  finalize unit sandboxing around it.
- **M4:** replace the sentinel with real per-repo, repository-scoped machine-user
  tokens, one `LoadCredential=` line each.

## GitHub machine user + repo-scoped tokens (issue #6)

Confirmed 2026-07-17. Delivers the first real token that #5 left as a sentinel.

### Org + machine user

- **Machine user:** `iQonAi-Bot` — a dedicated bot account (GitHub has no special
  "machine user" type; it is a normal account used only for automation).
  Attribution and credential isolation from the human `qdrtech` are the point.
- **Org:** `iQonAi` (login `iqonai`; owner `qdrtech`; belongs to QDR Ventures LLC,
  dba iQonAi). `iQonAi-Bot` is an org **Member** and a **Write** collaborator on
  each managed repo (Write = minimal role that allows push + open PR).
- **Repos moved into the org:** `agent-task` (done here → `iQonAi/agent-task`).
  `claude-agent-config` and `case-tracker-fc` are deferred.

**Why an org was required (not just the personal account + machine user).**
Fine-grained PATs can only target resources owned by the **token creator's own
account or an org they belong to** — an _outside collaborator_ cannot mint a
fine-grained token for another personal user's repo (documented GitHub
limitation). With the repos under personal `qdrtech`, `iQonAi-Bot` could not
mint repo-scoped tokens for them. Moving the repos into the `iQonAi` org (with
`iQonAi-Bot` as a member) makes the org the token's resource owner, so
fine-grained per-repo tokens work as `docs/project/0003` requires.

### Token

One **fine-grained** token per repo, minted as `iQonAi-Bot`:

- **Resource owner:** the `iQonAi` org (not the bot's personal account).
- **Repository access:** only the one repo (`iQonAi/agent-task`).
- **Permissions:** Contents R/W, Pull requests R/W, Issues R/W, Metadata R.
  (Issues R/W is a deliberate grant beyond the `contents + PR` baseline so the
  bot can create/manage issues; still repo-scoped.)
- **Expiration:** 90 days — rotation is part of least privilege.

> **Gotcha — org approval makes a new token inert.** With the org's "require
> approval for fine-grained PATs" setting on, a freshly minted token has access
> to **nothing** (404 on every repo, even its own) until the org owner approves
> it: org `iQonAi` → Settings → Personal access tokens → **Pending requests** →
> Approve. A 404 (not 401) on the repo is the tell — the token authenticates but
> is unapproved.

### Storage (`token_ref`)

Stored on the VM at `/etc/agent-task/credentials/<token_ref>`, root-owned
`0600`, referenced by name (the config `token_ref`); **never committed**.
Naming: `gh-token-<repo>` → `gh-token-agent-task`. The `agent-taskd` unit's
`LoadCredential=` line points at it (replaces the #5 sentinel); deferred repos
each add one line as they join the org.

Write the token without echoing it into shell history or anywhere else — paste
straight into a root-owned file, stripping the trailing newline:

```bash
sudo sh -c 'umask 077; tr -d "\n" > /etc/agent-task/credentials/gh-token-agent-task'
# paste the token, Enter, Ctrl-D
sudo chmod 0600 /etc/agent-task/credentials/gh-token-agent-task
```

### Host-only invariant (D3)

The GitHub token is **host-only**: it lives on the host, delivered to the
`agent-taskd` daemon via `LoadCredential`, and is used **only** for host-side
operations (push branch, open PR, fetch issue). It is **never** placed in an
execution container, in the copied-in source, or in the DB. The only secret a
container ever receives is the model API key (M3), by env at launch.

### Validation (all confirmed)

- **Stored:** `gh-token-agent-task` is `-rw------- root root`, 93 bytes,
  `github_pat_` prefix.
- **LoadCredential delivery:** `systemd-run --uid=agent-taskd -p LoadCredential=…`
  delivers it as `-r-------- agent-taskd`, 93 bytes, in `$CREDENTIALS_DIRECTORY`.
- **Negative direct read:** `sudo -u agent-taskd cat …/gh-token-agent-task` →
  `Permission denied`.
- **Token valid + least-privilege:** `gh api repos/iQonAi/agent-task` (with the token
  in `GH_TOKEN`) → `iQonAi/agent-task`, permissions `pull:true push:true`,
  `admin:false maintain:false`.
- **Repo-scoped:** the same token → **404** on a repo outside its scope
  (`qdrtech/claude-agent-config`), confirming it cannot reach other repos.

### Rotation & deferred

- **Rotate** before the 90-day expiry: mint a new token (approve it), overwrite
  the `0600` file, restart the daemon (once it exists, M0+).
- **Deferred:** `gh-token-claude-agent-config` and `gh-token-case-tracker-fc`
  (added when those repos join the org); the model API key (M3). Migration to a
  **GitHub App** (short-lived installation tokens) remains an option if long-lived
  PATs become a burden.

## M0 orchestrator deploy (issue #8)

Confirmed on the VM (verified 2026-07-21). The `agent-task` binary now exists, so
the unit installed in #5 is finally **started**. This is the first time the
daemon runs as `agent-taskd` under systemd.

### Build & install

Built by the operator in `/opt/agent-task`, installed to `/usr/local/bin` as root.
Build as yourself, not as root — nothing about compiling needs privilege:

```bash
cd /opt/agent-task && git checkout main && git pull
go build -o /tmp/agent-task ./cmd/agent-task
sudo install -m 0755 -o root -g root /tmp/agent-task /usr/local/bin/agent-task
```

Keep the installed unit in sync with the repo copy (source of truth), which
matters after the org transfer — an older copy still pointed `Documentation=` at
the pre-transfer URL:

```bash
sudo install -m 0644 /opt/agent-task/deploy/systemd/agent-taskd.service \
  /etc/systemd/system/agent-taskd.service
sudo systemctl daemon-reload
```

### Config (`/etc/agent-task/config.yaml`)

`root:root 0644` — deliberately world-readable, because it holds **no secrets**.
`token_ref` names a `LoadCredential` entry (D11/D3); the token itself never
appears here. The daemon reads this file as `agent-taskd`, so it must not be
`0600 root`.

```yaml
socket_path: /run/agent-task/agent-task.sock
data_dir: /var/lib/agent-task
limits:
  max_concurrent: 2
  task_timeout: 30m
repos:
  - name: agent-task
    owner: iQonAi
    repo: agent-task
    default_branch: main
    token_ref: gh-token-agent-task
```

`socket_path` and `data_dir` match systemd's `RuntimeDirectory=`/`StateDirectory=`;
setting them explicitly keeps the file self-describing.

```bash
sudo systemctl enable --now agent-taskd
```

### Validation (all confirmed)

- **`Type=notify` readiness:** `Starting…` and `Started…` are logged in the same
  second. This is the real test of `sd_notify` — outside systemd `NOTIFY_SOCKET`
  is unset and the call is a no-op, so it had never executed for real. A broken
  notify shows up as a ~90s hang ending in a timeout, not an error.
- **Socket boundary:** `/run/agent-task/agent-task.sock` is
  `660 agent-taskd:agent-taskd`; `/run/agent-task` is `750 agent-taskd:agent-taskd`.
  The daemon chmods the socket explicitly after bind, because the process umask
  would otherwise decide a security-relevant mode.
- **Operator access:** `qdrtech` (member of `agent-taskd`) runs `agent-task repos`
  and `agent-task ls` successfully — registry seeded from config, empty task table.
- **Negative — the container-runner uid is locked out:**
  `sudo -u agentbox agent-task repos` → `connect: permission denied`, exit 1.
  `agentbox` is the deliberate blast-radius target (D-isolation); it must never
  be able to drive the orchestrator, and the socket mode enforces that
  independently of any application-level check.
- **State:** `/var/lib/agent-task/agent-task.db` is owned by uid 998
  (`agent-taskd`), inside the `0700` `StateDirectory`.
- **Restart:** `systemctl restart` re-opens the existing DB without re-applying
  migrations or duplicating registry rows; `repos` still lists exactly one entry.
- **Logs:** Go `slog` JSON lines land in the journal
  (`journalctl -u agent-taskd`), with a clean `shutting down` on stop.

### Notes & deferred

- The DB file is created `0644` (SQLite honours the default umask); it is
  protected today solely by the `0700` `StateDirectory`. Harmless while that
  holds — the DB stores prompts, task state, and summaries, never tokens — but
  the file mode should not be relied on if that directory mode ever loosens.
- **Upgrades** are `go build` → `install` → `systemctl restart`; there is no
  packaging step yet.
- **Deferred:** unit sandboxing (`NoNewPrivileges`, `ProtectSystem=strict`,
  syscall filters) still waits on M2's cross-user Podman hop; the socket is
  read-only API surface until task creation lands in M1+.

## M1 host repo/worktree manager (issue #9)

Confirmed on the VM (verified 2026-07-24). Host-side git state management —
the mirror cache and feature-branch worktrees — that M3+ drives per task. M1
adds **no** operator command yet; sync is triggered by the task lifecycle,
which lands later. Verified out-of-band with a throwaway program (below).

### What it does

- **Mirror cache** at `<data_dir>/mirrors/<name>.git` — a bare `--mirror` clone,
  cloned to a `.tmp` path and atomically renamed so an interrupted clone never
  leaves a directory that later looks like a valid cache. Re-runs `git fetch
  --prune` instead of re-cloning.
- **Worktrees** at `<data_dir>/worktrees/<task-id>`, each on a fresh
  `agent/<agent>/<slug>-<shortid>` branch off the mirror's default branch. The
  `agent/` prefix reserves the namespace for future GitHub branch-protection
  (design §8.3).
- **Hooks disabled on every host-side git call.** All git runs through
  `internal/gitx`, which injects `core.hooksPath=/dev/null`,
  `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null`, and
  `GIT_TERMINAL_PROMPT=0`, and builds the environment from scratch rather than
  inheriting the daemon's. Agent-authored content (copied-in source, bundles)
  is untrusted, and git executes code from the repo it operates on.
- **Orphan sweep** at daemon startup removes worktrees/branches for
  terminal-state tasks left by a previous run; best-effort and idempotent.

### Credential path (host-only, D3)

Private-repo clone/fetch authenticates as the machine user via a git credential
helper fed the token through the **environment**, never argv — `/proc/<pid>/cmdline`
is world-readable. The token is resolved from `$CREDENTIALS_DIRECTORY` (systemd
`LoadCredential`); auth is **optional**, so a public repo clones anonymously and
development runs outside systemd need no token. The token never enters a
container and never lands in `.git/config` or on disk.

### Validation

Unit tests cover clone/fetch, worktree create/remove, hook-disable (a planted
hook is proven **not** to fire), and sweep idempotency — all against local
`file://` origins, no network. The one path tests cannot reach — auth against
real `github.com` — was verified on the VM with a throwaway program
(`cmd/verify-m1`, **not committed**) run as the service user with the token
delivered exactly as the daemon will get it:

```bash
sudo systemd-run --uid=agent-taskd --pipe --wait -q \
  -p LoadCredential=gh-token-agent-task:/etc/agent-task/credentials/gh-token-agent-task \
  -p 'Environment=PATH=/usr/local/go/bin:/usr/bin:/bin' \
  /tmp/verify-m1
```

Output confirmed the full round trip:

```
mirror synced: /tmp/m1-verify/mirrors/agent-task.git
worktree: /tmp/m1-verify/worktrees/verify-task branch: agent/claude/m1-smoke-test-smoke01
worktree torn down OK
```

i.e. the machine-user token (delivered only via `LoadCredential`) cloned the
**private** `iQonAi/agent-task` through the credential helper, a worktree was
created off `main`, and teardown removed it. The throwaway and its scratch dir
(`/tmp/m1-verify`) were deleted afterward.

### Deferred (not this issue)

- **M3+:** task lifecycle drives `Sync` + worktree create per run; the sweep
  gains real tasks to act on.
- No operator-facing sync/worktree command yet — intentional; M1 is the library
  layer the controller calls.

## M2 container runner + isolation (issue #10)

Confirmed on the VM (verified 2026-07-30). The runner launches a disposable,
isolated container per task via rootless Podman as `agentbox`, copies source in,
runs the agent non-root under resource limits on the egress-restricted network,
and collects a `git bundle` + logs out as inert data. gVisor (`runsc`) is the
intended hardening layer but remains **validated-deferred to #17** (D5); M2 runs
on the default `crun` runtime, and `Spec.Runtime` is the swap point.

Everything below was proven by hand first (the #4 spike pattern), then encoded
in `internal/runner` and re-verified through the Go runner.

### Environment (Podman 3.4.4, cgroup v2)

- Podman **3.4.4**, runtime **crun**, rootless network **slirp4netns** (as #4).
- The base image `localhost/agent-task-base:dev` (#7) is in `agentbox`'s
  rootless storage.
- `agentbox` has `Linger=yes` so `/run/user/999` exists for rootless Podman.

### Cgroup delegation (required for CPU/memory limits)

On cgroup v2, non-root users get only `memory pids` delegated by default — **not
`cpu`** — so `--cpus` fails with *"the requested cgroup controller `cpu` is not
available"*. Delegate the controllers to every user manager:

```bash
sudo mkdir -p /etc/systemd/system/user@.service.d
sudo tee /etc/systemd/system/user@.service.d/delegate.conf >/dev/null <<'DROP'
[Service]
Delegate=cpu cpuset io memory pids
DROP
sudo systemctl daemon-reload
sudo systemctl restart user@999.service   # agentbox's user manager (uid 999)
cat /sys/fs/cgroup/user.slice/user-999.slice/user@999.service/cgroup.controllers
# want: cpuset cpu io memory pids
```

`user@999.service` is tied to `agentbox`'s uid; if that uid changes, so does the
unit name.

### Cross-user hop (daemon → agentbox)

The daemon runs as `agent-taskd` (uid 998, owns the token/DB); containers must
run as `agentbox` (uid 999) so #4's `iptables --uid-owner 999` egress deny-list
applies automatically — slirp4netns opens the container's sockets as the owning
uid. The daemon invokes Podman through a **root-owned wrapper** (which sets
agentbox's rootless env) plus a **narrow sudoers rule**. Setting the env inside
the wrapper — rather than `sudo … env HOME=… podman` — keeps the sudoers command
match exact; allowing `env` in the rule would let any command through.

```bash
sudo tee /usr/local/sbin/agentbox-podman >/dev/null <<'WRAP'
#!/bin/sh
# Run podman as agentbox with its rootless environment.
export HOME=/home/agentbox
export XDG_RUNTIME_DIR=/run/user/999
exec /usr/bin/podman "$@"
WRAP
sudo chown root:root /usr/local/sbin/agentbox-podman
sudo chmod 0755 /usr/local/sbin/agentbox-podman

sudo tee /etc/sudoers.d/agent-task-podman >/dev/null <<'SUDO'
agent-taskd ALL=(agentbox) NOPASSWD: /usr/local/sbin/agentbox-podman
qdrtech     ALL=(agentbox) NOPASSWD: /usr/local/sbin/agentbox-podman
SUDO
sudo chmod 0440 /etc/sudoers.d/agent-task-podman
sudo visudo -cf /etc/sudoers.d/agent-task-podman   # must print: parsed OK
```

The `qdrtech` line exists only to run the integration tests as the operator;
production uses the `agent-taskd` line. The runner pins its working directory to
`/` because the hop inherits the caller's cwd and `agentbox` cannot `chdir` into
the daemon's home or `/opt/agent-task` (mode 0700); all Podman paths are absolute.

### Transfer model (proven)

`podman create → cp source in → cp empty out-dir in → start -a → cp artifacts
out → rm` (container + volume, always). Podman **remaps ownership** on `cp` in
and out, so the container sees the source as its own uid and the host reads the
artifacts as `agentbox`. `cp` of a *missing* `/task/out` is a silent no-op on
3.4.4, so the runner seeds an empty `/task/out` before start.

### Validation (all confirmed through the Go runner)

```bash
cd /opt/agent-task && git checkout feat/10-m2-container-runner
RUNNER_PODMAN_BASE="sudo -u agentbox /usr/local/sbin/agentbox-podman" \
  go test -tags integration ./internal/runner -v
```

- **Round trip** — source copied in, a commit made inside a non-root,
  `--cap-drop ALL`, `no-new-privileges`, `--read-only` container, a
  `base..HEAD` bundle written out, and the host verifying that bundle as inert
  data (`git bundle verify`).
- **Egress** — from inside the hardened container: public HTTP `200` and public
  DNS resolve; `10.0.0.1` (and, hand-tested, all five denied ranges
  `10/8`, `172.16/12`, `192.168/16`, `100.64/10`, `169.254/16`) blocked.
- **Memory limit** — allocating 1 GB under a 128 MB cap is OOM-killed (non-zero
  exit), proving the limit is enforced, not swapped through.

### Deferred (not this issue)

- **#17:** flip `Spec.Runtime` to `runsc` (gVisor) once the spike validates its
  interop with slirp4netns + the egress rules.
- **M3:** the model API key crosses into the container by env at launch (must be
  passed through Podman's environment, never argv); the agent adapter owns the
  container command.
- **M5:** the daemon drives the runner per task (lifecycle, the 30-min timeout via
  the run context, container orphan sweep); the per-task out dir gains a
  shared-group home so `agent-taskd` reads what `agentbox` wrote.

## M3 Claude Code adapter — first end-to-end agent run (issue #11)

Confirmed on the VM (verified 2026-07-31). A real agent run: prompt → sync →
feature worktree → standalone source export → agent in the isolated container →
`git bundle` out → applied onto the host feature branch → artifacts. M3 stops at
"commits on the feature branch"; host push, the templated PR, and the GitHub
token are M4, and the daemon/worker-pool path is M5. The only new secret is the
**model key**; **no GitHub token is used anywhere in M3**.

### Model auth (both modes supported)

The agent adapter maps an auth method to an env var: subscription →
`CLAUDE_CODE_OAUTH_TOKEN`, api_key → `ANTHROPIC_API_KEY`. For a Claude
subscription, mint a one-year token on a machine with a browser:

```bash
claude setup-token         # prints the token; copy it
```

Store it on the VM as a 0600 file (kept out of shell history):

```bash
install -m 600 /dev/null ~/.claude-token
cat > ~/.claude-token      # paste, Enter, Ctrl-D
```

### Running a task

Standalone, in-process (`agent-task run`). The container runs as `agentbox`, so
scratch dirs are made agentbox-accessible; `--podman` injects the cross-user
wrapper; `--data-dir`/`--work-dir` are operator-writable paths (the daemon uses
its `StateDirectory` in M5).

```bash
/tmp/agent-task run \
  --agent claude --auth subscription \
  --repo-url file:///tmp/m3-testrepo --default-branch main \
  --task "Add a subtract(a, b) function to src/hello.py that returns a - b." \
  --podman "sudo -u agentbox /usr/local/sbin/agentbox-podman" \
  --data-dir /tmp/m3-data --work-dir /tmp/m3-work \
  --model-token-file ~/.claude-token
```

### Two findings that only surfaced on the real VM

- **`bash -c`, not `bash -lc`.** The container sets `HOME=/task`; a login shell
  sources `/etc/profile` and a non-existent `/task/.profile`, resetting `PATH`
  and dropping `/home/agent/.local/bin` where the agent CLIs live (agent came
  back exit 127). Non-login `-c` inherits the image's `ENV PATH`.
- **Model key via `--env-file`, not `podman -e NAME`.** Podman 3.4.4 did not
  forward the token with `-e NAME` pass-through, and sudo `env_keep` across the
  hop was unreliable, so Claude saw an empty token ("Not logged in"). The runner
  now writes `name=value` to a temp file and passes `--env-file <path>` — the
  value is in the file, only the path is in argv, and the file is removed once
  the container is created. (The earlier `env_keep` sudoers line is therefore no
  longer required.)

### Validation (confirmed)

- **Mock agent** (`--agent mock`, no token): `Completed`, 1 commit on the
  feature branch — proves the whole container pipeline (copy-in as agentbox,
  commit inside, bundle out, apply) cross-user.
- **Claude, subscription auth:** the agent added `subtract(a, b)` to
  `src/hello.py`, kept `hello()`, and committed
  (`Add subtract(a, b) function to hello.py`) on
  `agent/claude/<slug>-<id>`. Artifacts collected: transcript (json), summary,
  run log, diff, bundle.
- **Token isolation:** delivered by `--env-file` (never argv); no GitHub token
  present in the container.

### OQ-4 — source-copy cost

The export is a full `--no-hardlinks --single-branch` clone at the base commit,
so its size (and copy time) scales with repo size. Data point: the trivial test
repo produced a **204 KB** export. Negligible here; for a large monorepo this is
the cost to watch — revisit per OQ-4 (shallow/partial-clone options) before
onboarding big repos.

### Deferred

- **M4:** issue fetch → prompt (`--issue`); host push + templated PR; the
  machine-user repo-scoped GitHub token via `LoadCredential`.
- **M5:** daemon-driven runs (worker pool, cancellation, the 30-min timeout,
  restart recovery); the model key delivered via `LoadCredential` and the run
  scratch/out dirs moved to a shared-group home instead of world-accessible.

## M4 GitHub integration — issue → prompt → PR (issue #12)

Confirmed on the VM (verified 2026-07-31). **The bootstrap line.** A full task
now closes to a pull request: fetch the issue, render the prompt, run the agent
(M3), host-push the feature branch, open a templated PR, and back-link the
issue. The repo-scoped machine-user token is used **only** host-side in the
`github` package (D3) — never in a container, the export, the bundle, or argv.

### Token (host-only, D3)

The push, PR, issue fetch, and the private-repo mirror clone all use the same
repo-scoped `iQonAi-Bot` token. It is passed to `gh` via `GH_TOKEN` (not argv)
and to `git` via a one-shot credential helper (token in the environment, never
in `.git/config`). M4's standalone run reads it from a file; the daemon wires
`LoadCredential` in M5.

```bash
# make the root-owned machine-user token readable for a standalone run:
sudo install -m 600 -o qdrtech /etc/agent-task/credentials/gh-token-agent-task /tmp/gh-token
```

### Running an issue → PR

```bash
/tmp/agent-task run \
  --repo agent-task --issue 27 \
  --agent claude --auth subscription \
  --podman "sudo -u agentbox /usr/local/sbin/agentbox-podman" \
  --data-dir /tmp/m4-data --work-dir /tmp/m4-work \
  --model-token-file ~/.claude-token \
  --github-token-file /tmp/gh-token
```

### Two findings from the live run

- **`repo.Sync` takes a token value, not a `token_ref`.** The same token feeds
  the private clone and the push/PR, so the caller resolves the credential once
  and passes the value; `repo` no longer depends on `creds`. (The store still
  records the `token_ref` name for the daemon.)
- **Push to the explicit repo URL, not the `origin` remote.** The mirror is a
  `git clone --mirror`, so `remote.origin.mirror=true` rejects a single-branch
  refspec (*"--mirror can't be combined with refspecs"*). `github.Push` pushes
  to `https://github.com/<owner>/<repo>.git <branch>` with the credential helper.

### Validation (confirmed)

A throwaway test issue (create a one-line file) run end-to-end produced PR #28
on `iQonAi/agent-task`: authored by **`iQonAi-Bot`**, base `main`, head
`agent/claude/<slug>-<id>`, adding exactly `docs/agent-smoke.md`. The PR body
carried the template (task id, agent, issue link, summary) and the plain marker
**"Agent-produced — human review required. Do not merge without review."** A
back-link comment was posted to the issue. Never auto-merged; the test PR and
issue were closed afterward.

- **PR marker:** a no-emoji safety marker (not the "🤖 generated-with-AI"
  pattern), per operator policy — it flags agent authorship for the human gate
  without an AI-attribution tagline.
- **Token isolation:** unit-tested — the GitHub token never appears in the
  runner `Spec` handed to the container.

### Deferred

- **M5:** the daemon drives runs (worker pool, cancellation, timeout, recovery);
  the GitHub token via `LoadCredential` instead of a file; captured test output
  in the PR body; the run scratch/out dirs moved to a shared-group home.

## M5 lifecycle, concurrency, cancel, recovery (issue #13)

Confirmed on the VM (verified 2026-08-07). The daemon now drives tasks: `submit`
queues, a worker pool runs them (D10 cap = 2), the full state machine + audit
trail is recorded, `cancel` stops a run, and a restart recovers interrupted
tasks. This is the first milestone the box can develop itself.

### Deployment additions

- **Config** (`/etc/agent-task/config.yaml`): `image`, `podman` (the cross-user
  wrapper), `work_dir` (shared scratch root — see below), and an `agents:` block
  mapping each agent to an auth method + `token_ref`.
- **Model credential via LoadCredential** (M5, alongside the GitHub token):
  ```bash
  sudo install -m 600 -o root -g root ~/.claude-token \
    /etc/agent-task/credentials/claude-oauth-token
  ```
  and `LoadCredential=claude-oauth-token:...` in the unit; the config
  `agents.claude.token_ref` names it.
- **Shared scratch group.** Per-task work dirs can't live under the daemon's
  `0700` StateDirectory (the container uid `agentbox` can't traverse it), so they
  live under a shared root:
  ```bash
  sudo groupadd -f agentwork
  sudo usermod -aG agentwork agent-taskd
  sudo usermod -aG agentwork agentbox
  sudo install -d -m 2770 -o agent-taskd -g agentwork /var/lib/agent-work
  ```
  The unit gains `SupplementaryGroups=agentwork`; the submitter creates per-task
  dirs `2770+setgid` so the group propagates to podman-written files and the
  daemon can read back what `agentbox` wrote.

### Two findings that only surfaced with the daemon driving podman

- **`ProtectHome=yes` / `PrivateTmp=yes` break the cross-user podman hop.** The
  daemon runs `sudo -u agentbox podman` in its **own** mount namespace, which the
  sudo child inherits. `ProtectHome` hides `/home/agentbox` — where agentbox's
  rootless podman storage lives — so `podman create` fails and the task hung.
  Both directives are removed; the container (not the unit) is the isolation
  boundary. NoNewPrivileges stays off too (it would block the sudo hop).
- **`gh pr create` must run inside the worktree.** It shells out to `git`, and
  the daemon's cwd is `/` (`fatal: not a git repository`). It now runs with the
  worktree as its working directory. (`gh issue view`/`comment` are API-only and
  cwd-independent.)
- Also fixed: a pipeline error from the controller was discarded by the pool,
  leaving a task stuck in `Running`. The daemon now records it as `Failed` with
  the reason and logs it — otherwise a failure is invisible.

### Running / observing tasks

```bash
agent-task submit --repo agent-task --agent claude --task "…"   # → task id
agent-task submit --repo agent-task --agent claude --issue N    # issue → prompt
agent-task ls                                               # recent tasks + state
agent-task status <id>                                      # state + full audit trail
agent-task cancel <id>                                      # stop a running task
```

### Validation (all confirmed live)

- **End-to-end:** a `submit` ran `Created→Running`→ phases (sync, worktree,
  export, run agent, apply bundle) → `pushed branch` → `Opened PR` → `Completed`,
  opening a real PR as `iQonAi-Bot`. The audit trail is visible via `status`.
- **Concurrency cap (D10):** three submissions → `ls` showed exactly **2 Running
  + 1 Created** (queued); the third started only when a slot freed.
- **Cancel:** `cancel` mid-run stopped the container and ended the task
  `Cancelled` (`context.Canceled`, distinct from the timeout's `Failed`).
- **Restart recovery:** a task interrupted by `systemctl restart` was marked
  `Failed` ("interrupted by daemon restart") on the next start, and its worktree
  swept.

Live-test PRs/branches were closed and deleted afterward.

### Deferred

- Streaming logs (`agent-task logs <id> -f`) and richer `status` (current phase,
  PR link inline) — the `obs` seams are left, not built (§11).
- Re-adding tighter unit sandboxing once the runner's isolation mechanism is
  finalized (post-#17): whatever is compatible with the cross-user podman hop.
