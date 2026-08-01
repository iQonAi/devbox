package controller

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iQonAi/devbox/internal/agent"
	"github.com/iQonAi/devbox/internal/gitx"
	"github.com/iQonAi/devbox/internal/prompt"
	"github.com/iQonAi/devbox/internal/repo"
	"github.com/iQonAi/devbox/internal/runner"
)

// localRunner simulates the container on the host: it applies the agent's effect
// to the export dir and produces a bundle in OutDir, exactly as the real
// container would. It lets us test the controller's export→apply orchestration
// without Podman (the real container path has its own integration tests).
type localRunner struct{ exitCode int }

func (l localRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	dir := spec.SourceDir
	base, err := gitx.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return runner.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "change.txt"), []byte("agent change\n"), 0o644); err != nil {
		return runner.Result{}, err
	}
	if _, err := gitx.Run(ctx, dir, "add", "change.txt"); err != nil {
		return runner.Result{}, err
	}
	if _, err := gitx.Run(ctx, dir, "commit", "-m", "agent change"); err != nil {
		return runner.Result{}, err
	}
	if err := os.MkdirAll(spec.OutDir, 0o755); err != nil {
		return runner.Result{}, err
	}
	if _, err := gitx.Run(ctx, dir, "bundle", "create",
		filepath.Join(spec.OutDir, "changes.bundle"), base+"..HEAD"); err != nil {
		return runner.Result{}, err
	}
	_ = os.WriteFile(filepath.Join(spec.OutDir, "transcript.json"), []byte(`{"result":"done"}`), 0o644)
	return runner.Result{ExitCode: l.exitCode}, nil
}

func initOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(t, dir, "add", "README.md")
	must(t, dir, "commit", "-m", "initial")
	return dir
}

func must(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := gitx.Run(context.Background(), dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestRunCompletesAndAppliesCommits(t *testing.T) {
	origin := initOrigin(t)
	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: localRunner{exitCode: 0}, Image: "unused"},
		Request{
			TaskID: "task-1", Title: "add change", RepoName: "devbox",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "make a change"},
			Agent:  agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateCompleted {
		t.Errorf("state = %q, want Completed", out.State)
	}
	if out.Commits != 1 {
		t.Errorf("commits = %d, want 1", out.Commits)
	}
	if _, err := os.Stat(filepath.Join(out.Worktree, "change.txt")); err != nil {
		t.Errorf("agent change not applied to feature branch: %v", err)
	}
	if len(out.Artifacts) == 0 {
		t.Error("no artifacts collected")
	}
}

// blockedRunner simulates an agent that never finishes: it blocks until the
// run context is cancelled and returns the context's error, like the real
// Podman runner does when the wall-clock timeout kills the container.
type blockedRunner struct{}

func (blockedRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	<-ctx.Done()
	return runner.Result{}, ctx.Err()
}

// A run that hits the wall-clock timeout is a Failed task (D9), not a
// pipeline error.
func TestRunTimeoutIsFailed(t *testing.T) {
	origin := initOrigin(t)
	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: blockedRunner{}, Image: "x"},
		Request{
			TaskID: "task-timeout", Title: "x", RepoName: "devbox",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
			Limits:  Limits{Timeout: 50 * time.Millisecond},
		})
	if err != nil {
		t.Fatalf("run: %v (a timeout must not be a pipeline error)", err)
	}
	if out.State != StateFailed {
		t.Errorf("state = %q, want Failed", out.State)
	}
	if out.Error != "timeout" {
		t.Errorf("error = %q, want %q", out.Error, "timeout")
	}
}

// badBundleRunner leaves a garbage changes.bundle so ApplyBundle fails.
type badBundleRunner struct{}

func (badBundleRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	if err := os.MkdirAll(spec.OutDir, 0o755); err != nil {
		return runner.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(spec.OutDir, "changes.bundle"), []byte("not a bundle"), 0o644); err != nil {
		return runner.Result{}, err
	}
	return runner.Result{ExitCode: 0}, nil
}

// An unappliable bundle is Failed, and the apply error text is surfaced.
func TestRunApplyFailureSurfacesError(t *testing.T) {
	origin := initOrigin(t)
	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: badBundleRunner{}, Image: "x"},
		Request{
			TaskID: "task-badbundle", Title: "x", RepoName: "devbox",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateFailed {
		t.Errorf("state = %q, want Failed", out.State)
	}
	if !strings.Contains(out.Error, "bundle") {
		t.Errorf("error = %q, want the apply error text", out.Error)
	}
}

// symlinkRunner drops symlinked artifacts in OutDir, as a hostile agent could
// via podman cp's symlink-preserving copy-out.
type symlinkRunner struct{ target string }

func (r symlinkRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	if err := os.MkdirAll(spec.OutDir, 0o755); err != nil {
		return runner.Result{}, err
	}
	for _, name := range []string{"changes.bundle", "transcript.json"} {
		if err := os.Symlink(r.target, filepath.Join(spec.OutDir, name)); err != nil {
			return runner.Result{}, err
		}
	}
	return runner.Result{ExitCode: 0}, nil
}

// Symlinked artifacts are never indexed, and a symlinked bundle is rejected
// instead of applied (it could alias any host file).
func TestRunRejectsSymlinkArtifacts(t *testing.T) {
	origin := initOrigin(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("host secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: symlinkRunner{target: secret}, Image: "x"},
		Request{
			TaskID: "task-symlink", Title: "x", RepoName: "devbox",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateFailed {
		t.Errorf("state = %q, want Failed", out.State)
	}
	if !strings.Contains(out.Error, "non-regular") {
		t.Errorf("error = %q, want a non-regular-artifact rejection", out.Error)
	}
	if len(out.Artifacts) != 0 {
		t.Errorf("artifacts = %v, want none (symlinks excluded)", out.Artifacts)
	}
	if out.Commits != 0 {
		t.Errorf("commits = %d, want 0 (bundle must not be applied)", out.Commits)
	}
}

// A non-zero agent exit is Failed even though commits were produced (D9).
func TestRunFailsOnNonzeroAgent(t *testing.T) {
	origin := initOrigin(t)
	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: localRunner{exitCode: 1}, Image: "x"},
		Request{
			TaskID: "task-2", Title: "x", RepoName: "devbox",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateFailed {
		t.Errorf("state = %q, want Failed", out.State)
	}
}
