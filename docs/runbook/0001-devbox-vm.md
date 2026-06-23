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
- Exact release: confirm with `lsb_release -ds` and pin it here for
  reproducibility.
- Post-install: `sudo apt-get update && sudo apt-get upgrade`, then installed
  baseline tooling (`curl`, `jq`, `wget`, `qemu-guest-agent`, ...).

## Provisioning checklist (issue #2)

Run these on the VM as the operator user; record outputs here as each item is
confirmed.

1. **Non-root operator user + unprivileged user namespaces**

   ```bash
   id                                            # non-root uid, member of sudo
   sysctl kernel.unprivileged_userns_clone       # expect 1
   grep "^$(whoami):" /etc/subuid /etc/subgid    # subuid/subgid ranges present
   ```

   On Ubuntu 24.04+, AppArmor restricts unprivileged userns by default; this
   affects the later rootless-Podman install, not this issue:

   ```bash
   sysctl kernel.apparmor_restrict_unprivileged_userns   # 1 = restricted
   ```

2. **Reachable on LAN** (for later Tailscale / SSH setup)

   ```bash
   ip -4 addr show scope global                  # record the LAN IP
   systemctl is-enabled ssh                       # ssh enabled, or install openssh-server
   ```

3. **Outbound internet**

   ```bash
   curl -fsSI https://github.com >/dev/null && echo "outbound OK"
   ```

4. **This runbook** records sizing and OS choice (above).
