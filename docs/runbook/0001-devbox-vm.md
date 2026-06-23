# 0001 — Devbox VM Operator Runbook

Operator (HITL) procedures for the Devbox VM that hosts the agent orchestrator
and execution containers. This VM is provisioned and maintained **by hand**: the
agent platform must never have Proxmox access (`docs/project/0003`). Proxmox is
human-only.

Related: issue #2 (provisioning), `docs/project/0002` (architecture / sizing),
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
