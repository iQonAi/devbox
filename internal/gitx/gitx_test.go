package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// InitRepo creates a repo with one commit and returns its path. Exported for
// reuse by later M1 packages as a local origin - no network in tests.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	// -b main: pin the initial branch insteaad of inheriting git's default.
	// which varies by version and config.
	if _, err := Run(ctx, dir, "init", "-b", "main"); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Run(ctx, dir, "add", "README.md"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := Run(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return dir
}

// The whole point of the package: a hook planted in a repo must not run.
func TestHooksDoNotFire(t *testing.T) {
	ctx := context.Background()
	dir := initRepo(t)
	marker := filepath.Join(dir, "hook-fired")

	hook := filepath.Join(dir, ".git", "hooks", "pre-commit")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Run(ctx, dir, "add", "second.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := Run(ctx, dir, "commit", "-m", "second"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("pre-commit hook executed: core.hookPath is not being applied")
	}
}

// Guard the setting itself, so a refactor that drops the -c flag fails here
// rather than silently re-enabling hook execution
func TestHooksPathIsSet(t *testing.T) {
	got, err := Run(context.Background(), initRepo(t), "config", "--get", "core.hooksPath")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if got != "/dev/null" {
		t.Errorf("core.hooksPath = %q, want /dev/null", got)
	}
}

// Commits must be attributable whithout any ambient user config present.
func TestIdentityIsSelfContained(t *testing.T) {
	got, err := Run(context.Background(), initRepo(t), "log", "-1", "--format=%an <%ae>")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if got != "agent-task <agent-task@localhost>" {
		t.Errorf("author = %q", got)
	}
}

// A git failure must surface git's own stderr, not just "exit status 1".
func TestErrorIncludesStderr(t *testing.T) {
	_, err := Run(context.Background(), initRepo(t), "checkout", "no-such-branch")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Errorf("error lost git's stderr: %v", err)
	}
}
