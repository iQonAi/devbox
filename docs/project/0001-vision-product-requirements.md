# 0001 - Agent Devbox Platform Vision & Product Requirements

## Overview

Agent Devbox is a self-hosted platform for running autonomous coding agents in isolated execution environments.

The platform allows a user to create development sessions from any device connected through Tailscale. Each session receives a dedicated workspace and disposable execution environment where coding agents can perform implementation work with elevated permissions without risking the user's workstation, personal files, home infrastructure, or production systems.

## Problem Statement

Current coding agents are typically executed directly on a developer workstation.

This creates several concerns:

- Agents require dangerous permissions to be effective
- Agents have access to personal files
- Agents may access credentials unintentionally
- Multiple agents can interfere with one another
- Long-running tasks require keeping a local machine online
- There is no centralized execution environment

## Goals

### Functional Goals

- Run coding agents remotely
- Support Claude Code
- Support Codex
- Support Pi
- Execute tasks from GitHub issues
- Create isolated workspaces per task
- Produce branches and pull requests
- Persist logs and execution history
- Access from any Tailscale-connected device

### Non-Goals (V1)

- Multi-agent orchestration
- Long-term memory
- Autonomous deployment
- Production system access
- Distributed execution
- Multi-user support

## Core Workflow

1. User submits repository + issue
2. System creates task record
3. System creates branch and worktree
4. System launches disposable container
5. Agent executes
6. Logs are persisted
7. Branch is pushed
8. Pull request is created
9. Container is destroyed

## Human Approval Model

Agents may:
- Read repository contents
- Modify repository contents
- Install dependencies
- Run tests
- Execute local development services

Agents may not:
- Merge pull requests
- Deploy infrastructure
- Access production systems
- Access production secrets

All changes must flow through pull request review.

## Success Criteria

A user can SSH into the devbox and execute:

agent-task run --repo case-tracker-fc --issue 34 --agent claude

The system produces:
- Branch
- Pull request
- Execution logs
- Summary
- Test results
