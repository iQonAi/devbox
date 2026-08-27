package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iQonAi/agent-task/internal/gitx"
	"github.com/iQonAi/agent-task/internal/store"
)

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// initOrigin builds a local repo to act as a remote, so tests never touch the
// network or need credentials.
func initOrigin(t *testing.T) (dir, url string) {
	t.Helper()
	dir = t.TempDir()
	mustGit(t, dir, "init", "-b", "main")
	commit(t, dir, "README.md", "hello\n", "initial")
	return dir, "file://" + dir
}

func commit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-m", msg)
}

func TestSyncClonesMirror(t *testing.T) {
	origin, url := initOrigin(t)
	m := NewManager(t.TempDir())

	path, err := m.Sync(context.Background(), "agent-task", url, "")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if path != m.MirrorPath("agent-task") {
		t.Errorf("path = %q, want %q", path, m.MirrorPath("agent-task"))
	}
	if got := mustGit(t, path, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Errorf("mirror is not bare: %q", got)
	}
	if got, want := mustGit(t, path, "rev-parse", "refs/heads/main"), mustGit(t, origin, "rev-parse", "HEAD"); got != want {
		t.Errorf("mirror main = %s, origin HEAD = %s", got, want)
	}
	// The staging directory must not survive a successful clone.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp clone dir left behind: %v", err)
	}
}

// The second call must fetch into the existing cache, not re-clone, and must
// pick up commits pushed to the origin in between.
func TestSyncFetchesOnSecondCall(t *testing.T) {
	origin, url := initOrigin(t)
	m := NewManager(t.TempDir())
	ctx := context.Background()

	if _, err := m.Sync(ctx, "agent-task", url, ""); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	commit(t, origin, "second.txt", "x\n", "second")

	path, err := m.Sync(ctx, "agent-task", url, "")
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got, want := mustGit(t, path, "rev-parse", "refs/heads/main"), mustGit(t, origin, "rev-parse", "HEAD"); got != want {
		t.Errorf("mirror not updated: %s != %s", got, want)
	}
}

// Auth is optional: a token_ref that resolves to nothing must still clone.
func TestSyncWithoutCredential(t *testing.T) {
	_, url := initOrigin(t)
	t.Setenv("CREDENTIALS_DIRECTORY", "")

	if _, err := NewManager(t.TempDir()).Sync(context.Background(), "agent-task", url, "gh-token-agent-task"); err != nil {
		t.Fatalf("sync without credential: %v", err)
	}
}

func TestCloneURL(t *testing.T) {
	got := CloneURL(store.Repo{Owner: "iQonAi", Repo: "agent-task"})
	if want := "https://github.com/iQonAi/agent-task.git"; got != want {
		t.Errorf("CloneURL = %q, want %q", got, want)
	}
}
