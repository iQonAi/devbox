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
