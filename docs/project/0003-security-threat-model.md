# 0003 - Agent-Task Security & Threat Model

## Security Philosophy

Coding agents are treated as untrusted code.

Assume:
- Agents can execute arbitrary commands
- Agents can install arbitrary dependencies
- Prompts may contain malicious instructions
- Agents may make mistakes

The platform must remain safe even when agents behave unexpectedly.

## Trust Boundaries

Level 1
User devices

Level 2
Agent-Task VM

Level 3
Execution containers

Level 4
Internet

Containers are the least trusted component.

## GitHub Access

Requirements:
- Dedicated machine user
- Fine-grained repository tokens
- Repository-scoped permissions

Prohibited:
- Personal GitHub tokens
- Organization admin tokens
- Broad write access

## Secrets

Allowed:
- Development credentials
- Repository credentials
- Model API keys

Prohibited:
- Production credentials
- Cloud admin credentials
- Personal SSH keys

## Network Restrictions

Execution containers may access:
- GitHub
- Package registries
- Model providers

Execution containers may not access:
- Proxmox
- TrueNAS
- Jellyfin
- Home LAN
- Internal databases

## Tailscale Model

User Devices → Agent-Task VM
Agent-Task VM → Internet
Containers → Internet

Containers must not become a bridge into the home network.

## Human Approval Gates

Required before:
- Merge
- Deploy
- Infrastructure changes
- Secret changes

All code changes require pull request review.

## Success Criteria

Compromise of a container must not provide:
- Proxmox access
- NAS access
- Production secrets
- Personal workstation access
