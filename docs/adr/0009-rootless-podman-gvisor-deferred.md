# ADR 0009 — Container runtime: rootless Podman now, gVisor validated-deferred (D5)

**Status:** Accepted (approved 2026-06-18; amended 2026-06-23 via #4/#17)

## Context

The container runs hostile code next to a home LAN (`docs/project/0003`,
Trust Boundaries). The runtime choice decides where a breakout lands.
Rootful Docker was explicitly rejected: a rootful breakout is host root.
Rootless Podman means a breakout lands as an unprivileged user — the
property that matters on this box — while still supporting gVisor
(`runsc`) first-class (`TECHNICAL_DESIGN.md:45`).

The original decision shipped gVisor day one. The **2026-06-23 amendment**
(issues #4/#17) moved it to *validated-deferred*: the highest-severity
control is the egress deny-list (R2), which is independent of the runtime;
gVisor + rootless networking has known rough edges (R1); and the Devbox VM
is already a hypervisor guest, so the host kernel is not the only boundary
below the container. gVisor's overhead and interop get measured in their
own spike (#17) and it flips on only if the move is cheap.

## Decision

- **Rootless Podman** is the engine. In production the daemon reaches it
  through a cross-user hop — `sudo -u agentbox` via a wrapper — so
  containers run as a dedicated unprivileged uid, never the daemon's or
  the operator's (`internal/runner/podman.go:84-86`,
  `config.example.yaml:20`, `deploy/systemd/agent-taskd.service:51-59`).
- The runner is **swappable behind one interface** so the runtime can
  change without touching the controller
  (`internal/runner/runner.go:44-47`). The gVisor flip is a single field:
  `Spec.Runtime = "runsc"` once #17 lands
  (`internal/runner/runner.go:35`, `internal/runner/podman.go:42-45`).
- Hardening is enforced by the runner, unconditionally — not configurable,
  so no caller can weaken the sandbox (`internal/runner/runner.go:8-19`):
  non-root user `10001`, `--cap-drop ALL`,
  `--security-opt no-new-privileges`, read-only rootfs, tmpfs `/tmp`, one
  writable per-task volume, `slirp4netns` networking, DNS pinned to a
  public resolver (`internal/runner/podman.go:26-45`).
- The **egress deny-list** — the R2 control this amendment prioritized over
  gVisor — is enforced host-side by uid: `slirp4netns` opens the
  container's egress sockets from the host namespace as the uid running
  Podman, so `iptables --uid-owner agentbox` REJECT rules deny RFC1918,
  the Tailscale CGNAT range, and link-local (including cloud metadata) for
  every agent container at once
  (`docs/runbook/0001-devbox-vm.md:121-166`). This is why the cross-user
  hop is load-bearing and not just tidiness.
- Disposability: one fresh container and volume per task, always torn down
  in a deferred cleanup that survives cancellation, with crash leftovers
  removed by deterministic name (`internal/runner/podman.go:249-271`,
  `:207-218`).

## Consequences

- A breakout lands as unprivileged `agentbox` inside a hypervisor guest,
  fenced by the egress deny-list — no home LAN, no tailnet, no metadata
  endpoint (`TECHNICAL_DESIGN.md:363`).
- Until #17 lands, agent syscalls hit the host kernel directly; the
  userspace-kernel layer is the named missing piece, not an unknown.
  Durable sessions raise its priority: ADR 0002 (Proposed) records breakout
  dwell time as R7 and names gVisor the mitigation.
- The daemon's systemd unit stays deliberately under-sandboxed
  (`ProtectHome`, `PrivateTmp`, `NoNewPrivileges` all off) because the
  cross-user Podman hop breaks under namespace isolation — the container,
  not the daemon unit, is the isolation boundary
  (`deploy/systemd/agent-taskd.service:51-59`).
- The distro pinned the details: Podman 3.4.4 means `slirp4netns` (no
  pasta) and `iptables` (no nftables) — recorded with the verification
  evidence in the runbook (`docs/runbook/0001-devbox-vm.md:127-136`).
