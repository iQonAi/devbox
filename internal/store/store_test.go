package store

import (
	"path/filepath"
	"testing"
)

// A fresh Open should create the schema and record exactly one migration.
func TestOpenMigrates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	for _, table := range []string{"repos", "tasks", "task_events", "artifacts"} {
		var name string
		if err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' and name=?`, table,
		).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations = %d, want 1", count)
	}
}

// The DSN pragma must actually switch the journal to WAL (a typo'd pragma key
// is silently ignored by the driver).
func TestOpenEnablesWAL(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// Re-opening the same DB must not re-apply migrations (idempotent).
func TestMigrateIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	s1.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer s2.Close()

	var count int
	if err := s2.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("after reopen migrations = %d, want 1", count)
	}
}

func TestUpsertAndListRepos(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	must := func(name, owner string) {
		if err := s.UpsertRepo(Repo{
			Name:          name,
			Owner:         owner,
			Repo:          name,
			DefaultBranch: "main",
			TokenRef:      "gh-token-" + name,
		}); err != nil {
			t.Fatalf("upsert %v : %v", name, err)
		}
	}

	must("agent-task", "iQonAi")
	must("app", "iQonAi")

	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2", len(repos))
	}

	if repos[0].Name != "agent-task" || repos[1].Name != "app" {
		t.Errorf("order = %q,%q; want agent-task, app", repos[0].Name, repos[1].Name)
	}

	// Re-upsert "agent-task" with a new owner; must UPDATE, not create a duplicate.
	must("agent-task", "changed")

	repos, _ = s.ListRepos()
	if len(repos) != 2 {
		t.Fatalf("after re-upsert got %d, want 2 (no duplicate)", len(repos))
	}
	for _, r := range repos {
		if r.Name == "agent-task" && r.Owner != "changed" {
			t.Errorf("owner not updated: %q", r.Owner)
		}
	}
}

func TestListTasksEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	tasks, err := s.ListTasks()
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("fresh DB has %d tasks, want 0", len(tasks))
	}
}

func TestSetMirrorPathSurvivesUpsert(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	base := Repo{Name: "agent-task", Owner: "iQonAi", Repo: "agent-task", DefaultBranch: "main", TokenRef: "t"}
	if err := s.UpsertRepo(base); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.SetMirrorPath("agent-task", "/var/lib/agent-task/mirrors/agent-task.git"); err != nil {
		t.Fatalf("set mirror path: %v", err)
	}

	// Re-seeding from config (as the daemon does on every start) must not
	// erase host state.
	if err := s.UpsertRepo(base); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if repos[0].MirrorPath != "/var/lib/agent-task/mirrors/agent-task.git" {
		t.Errorf("mirror_path clobbered by upsert: %q", repos[0].MirrorPath)
	}
}

func TestSetMirrorPathUnknownRepo(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if err := s.SetMirrorPath("nope", "/x"); err == nil {
		t.Fatal("expected an error for an unknown repo, got nil")
	}
}

// seedRepo inserts one repo and returns it's id.
func seedRepo(t *testing.T, s *Store) int64 {
	t.Helper()
	if err := s.UpsertRepo(Repo{Name: "agent-task", Owner: "iQonAi", Repo: "agent-task", DefaultBranch: "main", TokenRef: "t"}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if err := s.SetMirrorPath("agent-task", "/mirrors/agent-task.git"); err != nil {
		t.Fatalf("set mirror: %v", err)
	}
	repos, _ := s.ListRepos()
	return repos[0].ID
}

func TestCreateAndListTask(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)

	if err := s.CreateTask(NewTask{
		ID: "task-1", RepoID: repoID, Source: "manual", Agent: "claude",
		Branch: "agent/claude/x-abc1234", HostWorktree: "/wt/task-1",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tasks, err := s.ListTasks()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "task-1" || tasks[0].State != StateCreated {
		t.Fatalf("got %+v", tasks)
	}
}

// A task's FK to a non-existent repo must be rejected - proves foreign_keys is
// actually on (the DSN pragma), not silently ignored.
func TestCreateTaskBadRepoFails(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if err := s.CreateTask(NewTask{ID: "x", RepoID: 999, Source: "manual"}); err == nil {
		t.Fatalf("expected FK violation for unknown repo_id, got nil")
	}
}

// SetTaskBranchWorktree makes a daemon-submitted task (created with no branch
// or worktree) visible to the sweep once terminal.
func TestSetTaskBranchWorktree(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)

	if err := s.CreateTask(NewTask{ID: "task-1", RepoID: repoID, Source: "task"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetTaskBranchWorktree("task-1", "agent/claude/x-abc", "/wt/task-1"); err != nil {
		t.Fatalf("set branch/worktree: %v", err)
	}
	if err := s.UpdateTaskState("task-1", StateFailed); err != nil {
		t.Fatalf("state: %v", err)
	}

	got, err := s.SweepableTasks()
	if err != nil {
		t.Fatalf("sweepable: %v", err)
	}
	if len(got) != 1 || got[0].Branch != "agent/claude/x-abc" || got[0].HostWorktree != "/wt/task-1" {
		t.Fatalf("sweepable = %+v, want the updated branch/worktree", got)
	}

	if err := s.SetTaskBranchWorktree("nope", "b", "/w"); err == nil {
		t.Error("expected error for unknown task id")
	}
}

func TestSweepableTasksOnlyTerminalWithWorktree(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)

	mk := func(id, state, worktree string) {
		if err := s.CreateTask(NewTask{ID: id, RepoID: repoID, Source: "manual", Branch: "b-" + id, HostWorktree: worktree}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if state != StateCreated {
			if err := s.UpdateTaskState(id, state); err != nil {
				t.Fatalf("state %s: %v", id, err)
			}
		}
	}

	mk("done", StateCompleted, "/wt/done")  // sweepable
	mk("failed", StateFailed, "/wt/failed") // sweepable
	mk("running", StateRunning, "/wt/run")  // live: skip
	mk("nowt", StateCompleted, "")          // terminal but no worktree: skip

	got, err := s.SweepableTasks()
	if err != nil {
		t.Fatalf("sweepable: %v", err)
	}
	ids := map[string]bool{}
	for _, task := range got {
		ids[task.ID] = true
		if task.MirrorPath != "/mirrors/agent-task.git" {
			t.Errorf("%s mirror = %q", task.ID, task.MirrorPath)
		}
	}
	if len(got) != 2 || !ids["done"] || !ids["failed"] {
		t.Errorf("sweepable = %v, want {done, failed}", ids)
	}
}

func TestUpdateTaskStateUnknown(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.UpdateTaskState("nope", StateFailed); err == nil {
		t.Fatal("expected an error for unknown task, got nil")
	}
}

func TestArtifacts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)
	if err := s.CreateTask(NewTask{ID: "task-1", RepoID: repoID, Source: "task"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	for _, a := range []struct{ kind, path string }{
		{"bundle", "/out/changes.bundle"},
		{"transcript", "/out/transcript.json"},
		{"diff", "/out/diff.patch"},
	} {
		if err := s.InsertArtifact("task-1", a.kind, a.path); err != nil {
			t.Fatalf("insert %s: %v", a.kind, err)
		}
	}

	arts, err := s.ListArtifacts("task-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(arts) != 3 || arts[0].Kind != "bundle" || arts[2].Kind != "diff" {
		t.Fatalf("got %+v, want bundle/transcript/diff in order", arts)
	}
}

// An artifact for a non-existent task must be rejected (FK on).
func TestArtifactBadTaskFails(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if err := s.InsertArtifact("nope", "bundle", "/x"); err == nil {
		t.Fatal("expected FK violation for unknown task, got nil")
	}
}

func TestSetTaskPRURL(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)
	if err := s.CreateTask(NewTask{ID: "task-1", RepoID: repoID, Source: "manual"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	url := "https://github.com/iQonAi/agent-task/pull/42"
	if err := s.SetTaskPRURL("task-1", url); err != nil {
		t.Fatalf("set pr_url: %v", err)
	}
	var got string
	if err := s.db.QueryRow(`SELECT pr_url FROM tasks WHERE id = ?`, "task-1").Scan(&got); err != nil {
		t.Fatalf("read pr_url: %v", err)
	}
	if got != url {
		t.Errorf("pr_url = %q, want %q", got, url)
	}

	if err := s.SetTaskPRURL("nope", url); err == nil {
		t.Error("expected error for unknown task id")
	}
}

func TestEvents(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	repoID := seedRepo(t, s)
	if err := s.CreateTask(NewTask{ID: "task-1", RepoID: repoID, Source: "manual"}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	for _, e := range []struct{ typ, msg string }{
		{EventState, "Created→Running"},
		{EventPhase, "sync repo"},
		{EventSecurity, "container launched"},
	} {
		if err := s.InsertEvent("task-1", e.typ, e.msg); err != nil {
			t.Fatalf("insert %s: %v", e.typ, err)
		}
	}

	events, err := s.ListEvents("task-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 || events[0].Type != EventState || events[2].Message != "container launched" {
		t.Fatalf("got %+v, want 3 events in order", events)
	}

	// FK: an event for an unknown task must be rejected (foreign_keys on).
	if err := s.InsertEvent("nope", EventState, "x"); err == nil {
		t.Fatal("expected FK violation for unknown task, got nil")
	}
}
