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
		t.Errorf("scheme_migrations = %d, want 1", count)
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

	must("devbox", "iQonAi")
	must("app", "iQonAi")

	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2", len(repos))
	}

	if repos[0].Name != "app" || repos[1].Name != "devbox" {
		t.Errorf("order = %q,%q; want app, devbox", repos[1].Name, repos[1].Name)
	}

	// Re-upsert "devbox" with a new owner; must UPDATE, not create a duplicate.
	must("devbox", "changed")

	repos, _ = s.ListRepos()
	if len(repos) != 2 {
		t.Fatalf("after re-upsert got %d, want 3 (no duplicate)", len(repos))
	}
	for _, r := range repos {
		if r.Name == "devbox" && r.Owner != "changed" {
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
