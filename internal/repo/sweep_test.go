package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iQonAi/agent-task/internal/store"
)

// realStore gives a store backed by a temp DB with one repo whose mirror is
// synced through the same manager, so mirror/worktree paths line up.
func realStore(t *testing.T) (*store.Store, *Manager, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	_, url := initOrigin(t)
	m := NewManager(t.TempDir())
	mirror, err := m.Sync(context.Background(), "agent-task", url, "")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := s.UpsertRepo(store.Repo{Name: "agent-task", Owner: "iQonAi", Repo: "agent-task", DefaultBranch: "main", TokenRef: "t"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetMirrorPath("agent-task", mirror); err != nil {
		t.Fatalf("set mirror: %v", err)
	}
	return s, m, mirror
}

func repoID(t *testing.T, s *store.Store) int64 {
	t.Helper()
	repos, err := s.ListRepos()
	if err != nil || len(repos) == 0 {
		t.Fatalf("list repos: %v", err)
	}
	return repos[0].ID
}

func TestSweepRemovesTerminalWorktree(t *testing.T) {
	ctx := context.Background()
	s, m, mirror := realStore(t)
	id := repoID(t, s)

	branch := BranchName("claude", "done task", "abc1234")
	wt, err := m.AddWorktree(ctx, mirror, "task-done", branch, "main")
	if err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if err := s.CreateTask(store.NewTask{ID: "task-done", RepoID: id, Source: "manual", Agent: "claude", Branch: branch, HostWorktree: wt.Path}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := s.UpdateTaskState("task-done", store.StateCompleted); err != nil {
		t.Fatalf("state: %v", err)
	}

	n, err := m.Sweep(ctx, s)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d, want 1", n)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree survived sweep: %v", err)
	}

	// Second sweep: the record was cleared, so nothing to do.
	if n, err := m.Sweep(ctx, s); err != nil || n != 0 {
		t.Errorf("second sweep = (%d, %v), want (0, nil)", n, err)
	}
}

func TestSweepLeavesLiveTask(t *testing.T) {
	ctx := context.Background()
	s, m, mirror := realStore(t)
	id := repoID(t, s)

	branch := BranchName("claude", "running", "abc1234")
	wt, err := m.AddWorktree(ctx, mirror, "task-live", branch, "main")
	if err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if err := s.CreateTask(store.NewTask{ID: "task-live", RepoID: id, Source: "manual", Branch: branch, HostWorktree: wt.Path}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := s.UpdateTaskState("task-live", store.StateRunning); err != nil {
		t.Fatalf("state: %v", err)
	}

	n, err := m.Sweep(ctx, s)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("swept %d live tasks, want 0", n)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("live worktree was removed: %v", err)
	}
}
