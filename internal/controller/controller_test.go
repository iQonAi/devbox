package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
