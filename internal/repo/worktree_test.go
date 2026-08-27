package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// syncedMirror gives a manager with a mirror already cloned from a local origin.
func syncedMirror(t *testing.T) (m *Manager, mirrorPath string) {
	t.Helper()
	_, url := initOrigin(t)
	m = NewManager(t.TempDir())
	path, err := m.Sync(context.Background(), "agent-task", url, "")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	return m, path
}

func TestAddWorktree(t *testing.T) {
	m, mirror := syncedMirror(t)
	branch := BranchName("claude", "add feature", "abc1234")

	wt, err := m.AddWorktree(context.Background(), mirror, "task-1", branch, "main")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if wt.Path != m.WorktreePath("task-1") {
		t.Errorf("path = %q", wt.Path)
	}
	// The working tree is real and on the new branch.
	if _, err := os.Stat(filepath.Join(wt.Path, "README.md")); err != nil {
		t.Errorf("checkout missing README: %v", err)
	}
	if got := mustGit(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != branch {
		t.Errorf("HEAD = %q, want %q", got, branch)
	}
}

func TestRemoveWorktreeIsIdempotent(t *testing.T) {
	m, mirror := syncedMirror(t)
	ctx := context.Background()
	branch := BranchName("claude", "x", "abc1234")

	if _, err := m.AddWorktree(ctx, mirror, "task-1", branch, "main"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// First removal does the work; second must be a clean no-op.
	if err := m.RemoveWorktree(ctx, mirror, "task-1", branch); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	if _, err := os.Stat(m.WorktreePath("task-1")); !os.IsNotExist(err) {
		t.Errorf("worktree dir survived removal: %v", err)
	}
	if err := m.RemoveWorktree(ctx, mirror, "task-1", branch); err != nil {
		t.Errorf("second remove not idempotent: %v", err)
	}
}

// A worktree checkout is agent-controlled and therefore untrusted: a hook it
// plants must not fire during host-side git ope
func TestWorktreeHooksDisabled(t *testing.T) {
	m, mirror := syncedMirror(t)
	ctx := context.Background()
	wt, err := m.AddWorktree(ctx, mirror, "task-1", "agent/claude/x-abc1234", "main")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	marker := filepath.Join(wt.Path, "fired")
	hook := filepath.Join(mirror, "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatalf("plant hook: %v", err)
	}
	commit(t, wt.Path, "f.txt", "y\n", "in worktree")

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("hook fired inside worktree operation")
	}
}
