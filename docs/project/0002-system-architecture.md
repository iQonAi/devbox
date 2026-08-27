# 0002 - Agent-Task System Architecture

## Architecture

User Device
↓
Tailscale
↓
Agent-Task VM
↓
Task Controller
↓
Worktree Manager
↓
Execution Container
↓
GitHub

## Components

### Agent-Task VM

Responsibilities:
- Task orchestration
- Repository cache
- Worktree management
- Session persistence
- Agent execution lifecycle

Recommended Resources:
- 8 vCPU
- 16 GB RAM
- 200 GB SSD

### Task Controller

Responsibilities:
- Create tasks
- Track state
- Launch containers
- Collect artifacts
- Report results

State Machine:

Created → Running → Completed
Created → Running → Failed
Created → Cancelled

### Repository Manager

Responsibilities:
- Clone repositories
- Sync repositories
- Create worktrees
- Create branches

Rule:
One task = one branch = one worktree = one container

### Execution Containers

Containers are disposable.

Receive:
- Worktree mount
- Development environment
- Agent credentials

Do not receive:
- Host filesystem
- Home directory
- Production secrets

### Session Store

Stores:
- Task metadata
- Logs
- Agent transcripts
- Results
- Pull request links

## Future Architecture

Future versions may add:
- Discord control plane
- Agent memory
- Multi-agent orchestration
- Scheduling
- Shared project knowledge
