# 0001 — Devbox VM Operator Runbook

Operator (HITL) procedures for the Devbox VM that hosts the agent orchestrator
and execution containers. This VM is provisioned and maintained **by hand**: the
agent platform must never have Proxmox access (`docs/project/0003`). Proxmox is
human-only.

Related: issue #2 (provisioning), issue #3 (Tailscale + SSH),
`docs/project/0002` (architecture / sizing),
`docs/project/0003` (security & threat model).

## Host & sizing

| Property   | Value                          |
| ---------- | ------------------------------ |
| Hypervisor | Proxmox (human-only access)    |
| vCPU       | 8                              |
| RAM        | 16 GB                          |
| Disk       | 250 GB SSD                     |

`docs/project/0002` lists 200 GB SSD as the *recommended* minimum; this VM was
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
- Tailnet IP: `100.x.y.<redacted>` (host `devbox`).
- Host-only scoping confirmed: not an exit node, no subnet routes advertised,
  `ExitNodeAllowLANAccess=false` (`tailscale debug prefs`). Tailscale does not
  bridge the home LAN into the tailnet.
- **Invariant for later milestones:** containers must get their own isolated
  egress (Internet only) and must never see `tailscale0`. Do not advertise
  routes or run Tailscale inside a container.

### SSH (hardened, key-only)

- sshd listens on all interfaces (`0.0.0.0:22`, `[::]:22`) — reachable over both
  tailnet and LAN. ufw inactive.
- Hardening drop-in `/etc/ssh/sshd_config.d/10-devbox-hardening.conf` (sorts
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
never sees per-container source IPs (unlike rootful), but it *can* match the
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

| Target                          | Expected  | Result      |
| ------------------------------- | --------- | ----------- |
| Public DNS (`quad9.net`)        | resolves  | `dns-OK`    |
| Public HTTP (`icanhazip.com`)   | reachable | `public-OK` |
| `10.0.0.1` (RFC1918)            | blocked   | `blocked-OK`|
| `172.16.0.1` (RFC1918)          | blocked   | `blocked-OK`|
| home-LAN gateway (`<lan-gw>`)   | blocked   | `blocked-OK`|
| tailnet host (`100.64/10`)      | blocked   | `blocked-OK`|
| `169.254.169.254` (metadata)    | blocked   | `blocked-OK`|

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

| Tool | Version | Source |
| ---- | ------- | ------ |
| Go   | **1.26.5** | Official pinned tarball → `/usr/local/go` (see below) |
| git  | 2.34.1  | distro (`apt`) |
| gh   | 2.4.0   | distro (`apt`) — old (2022); upgrading via the official GitHub CLI apt repo is recommended but not required |

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

Cloned to **`/opt/devbox`** (owned by `qdrtech`) for building. The repo is
private, so it is cloned with the **operator's own `gh` identity** (`gh auth
login`, device flow) — deliberately **separate** from the daemon's machine-user
GitHub token (D3), which is never used for source and is wired only at M4.

```bash
sudo mkdir -p /opt/devbox && sudo chown "$USER:$USER" /opt/devbox
gh auth status || gh auth login
gh repo clone qdrtech/devbox /opt/devbox
```

### Service user (`agent-taskd`, distinct from `agentbox`)

The daemon runs as a dedicated system user **`agent-taskd`** (uid 998, own
group, `/usr/sbin/nologin`, home `/var/lib/agent-task`) — **not** `agentbox`.
Rationale: `agentbox` (uid 999) is the untrusted-container runner and the
deliberate blast-radius target; keeping the token/DB owner a *different* uid
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

- **M0:** build the binary (`go build -o /usr/local/bin/agent-task ./cmd/agent-task`),
  implement sd_notify readiness, then `systemctl enable --now`.
- **M2:** cross-user hop for the daemon to drive `agentbox`'s rootless Podman;
  finalize unit sandboxing around it.
- **M4:** replace the sentinel with real per-repo, repository-scoped machine-user
  tokens, one `LoadCredential=` line each.
