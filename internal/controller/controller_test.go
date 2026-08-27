package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iQonAi/agent-task/internal/agent"
	"github.com/iQonAi/agent-task/internal/gitx"
	"github.com/iQonAi/agent-task/internal/prompt"
	"github.com/iQonAi/agent-task/internal/repo"
	"github.com/iQonAi/agent-task/internal/runner"
	"github.com/iQonAi/agent-task/internal/store"
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
			TaskID: "task-1", Title: "add change", RepoName: "agent-task",
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
			TaskID: "task-timeout", Title: "x", RepoName: "agent-task",
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
			TaskID: "task-badbundle", Title: "x", RepoName: "agent-task",
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
			TaskID: "task-symlink", Title: "x", RepoName: "agent-task",
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
			TaskID: "task-2", Title: "x", RepoName: "agent-task",
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

// capturingRunner records the Spec it was handed and produces no bundle (so the
// run ends Failed with 0 commits, before any publish step).
type capturingRunner struct{ spec runner.Spec }

func (c *capturingRunner) Run(_ context.Context, spec runner.Spec) (runner.Result, error) {
	c.spec = spec
	return runner.Result{ExitCode: 0}, nil
}

// D3: the GitHub token is host-only and must never reach the container. Even
// with a token set (client constructed, mirror synced with it), it must not
// appear anywhere in the Spec handed to the runner.
func TestGitHubTokenNeverReachesRunner(t *testing.T) {
	origin := initOrigin(t)
	cr := &capturingRunner{}
	const secret = "ghp_SUPERSECRETTOKEN_should_not_leak"

	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: cr, Image: "x"},
		Request{
			TaskID: "tok", RepoName: "agent-task", RepoURL: "file://" + origin, DefaultBranch: "main",
			Owner: "iQonAi", Repo: "agent-task", GitHubToken: secret,
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	blob := fmt.Sprintf("%#v %v", cr.spec, cr.spec.Cmd)
	if strings.Contains(blob, secret) {
		t.Fatal("GitHub token leaked into the runner Spec")
	}
	// No commits (no bundle) → Failed → publish never attempted.
	if out.State != StateFailed {
		t.Errorf("state = %q, want Failed", out.State)
	}
}

// readArtifact must read a regular file (trimmed) but ignore a symlink — a
// hostile agent could symlink summary.txt at an arbitrary host file.
func TestReadArtifactIgnoresSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "summary.txt"), []byte("  hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readArtifact(dir, "summary.txt"); got != "hello" {
		t.Errorf("regular file = %q, want %q", got, "hello")
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("host secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkDir := t.TempDir()
	if err := os.Symlink(secret, filepath.Join(linkDir, "summary.txt")); err != nil {
		t.Fatal(err)
	}
	if got := readArtifact(linkDir, "summary.txt"); got != "" {
		t.Errorf("symlinked artifact read %q, want \"\" (must be ignored)", got)
	}
}

func TestRunRecordsStateAndPhaseEvents(t *testing.T) {
	origin := initOrigin(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// The task must exist first (FK); the daemon creates it before running
	if err := st.UpsertRepo(store.Repo{
		Name:          "agent-task",
		Owner:         "o",
		Repo:          "r",
		DefaultBranch: "main",
		TokenRef:      "t",
	}); err != nil {
		t.Fatal(err)
	}

	repos, _ := st.ListRepos()
	if err := st.CreateTask(store.NewTask{
		ID:     "task-ev",
		RepoID: repos[0].ID,
		Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := Run(context.Background(),
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: localRunner{exitCode: 0}, Image: "x", Recorder: st},
		Request{
			TaskID:        "task-ev",
			Title:         "add change",
			RepoName:      "agent-task",
			RepoURL:       "file://" + origin,
			DefaultBranch: "main",
			Prompt:        prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateCompleted {
		t.Fatalf("state = %q, want Completed", out.State)
	}

	events, err := st.ListEvents("task-ev")
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	// First Event is entering running and the last is the terminal transition
	if len(events) < 3 {
		t.Fatalf("got %d events, want state+phase trail", len(events))
	}
	if events[0].Type != store.EventState || events[0].Message != "Created->Running" {
		t.Errorf("first event = %+v, want Created->Running", events[0])
	}
	last := events[len(events)-1]
	if last.Type != store.EventState || last.Message != "->Completed" {
		t.Errorf("last event = %+v, want -> Completed", last)
	}
	// The security trail is honest (§12): launch attempt, launch, teardown,
	// and bundle extraction are all recorded.
	for _, want := range []string{"launching container", "container launched", "container destroyed", "bundle extracted"} {
		found := false
		for _, e := range events {
			if e.Type == store.EventSecurity && strings.Contains(e.Message, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing security event %q in %+v", want, events)
		}
	}

	// The store's persisted state matches the outcome
	tasks, _ := st.ListTasks()
	if tasks[0].State != StateCompleted {
		t.Errorf("persisted state = %q, want Completed", tasks[0].State)
	}

	// Branch + worktree were persisted mid-run (via the Recorder), so the
	// orphan sweep can find the terminal task's worktree.
	sweepable, err := st.SweepableTasks()
	if err != nil {
		t.Fatalf("sweepable: %v", err)
	}
	found := false
	for _, s := range sweepable {
		if s.ID == "task-ev" && s.Branch == out.Branch && s.HostWorktree == out.Worktree {
			found = true
		}
	}
	if !found {
		t.Errorf("task-ev not sweepable (branch/worktree not persisted): %+v", sweepable)
	}
}

// ContextOutcome maps context causes to terminal states (§7.4): timeout ->
// Failed, shutdown -> Failed with the recovery wording, user/plain cancel ->
// Cancelled, live context -> not ok.
func TestContextOutcome(t *testing.T) {
	live := context.Background()
	if _, _, ok := ContextOutcome(live); ok {
		t.Error("live context reported a terminal outcome")
	}

	timedOut, cancelT := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelT()
	<-timedOut.Done()
	if s, r, ok := ContextOutcome(timedOut); !ok || s != StateFailed || r != "timeout" {
		t.Errorf("timeout = (%q, %q, %v), want (Failed, timeout, true)", s, r, ok)
	}

	shut, cancelS := context.WithCancelCause(context.Background())
	cancelS(ErrShutdown)
	if s, r, ok := ContextOutcome(shut); !ok || s != StateFailed || r != "interrupted by daemon shutdown" {
		t.Errorf("shutdown = (%q, %q, %v), want (Failed, interrupted..., true)", s, r, ok)
	}

	userC, cancelU := context.WithCancelCause(context.Background())
	cancelU(ErrUserCancel)
	if s, r, ok := ContextOutcome(userC); !ok || s != StateCancelled || r != "cancelled" {
		t.Errorf("user cancel = (%q, %q, %v), want (Cancelled, cancelled, true)", s, r, ok)
	}

	plain, cancelP := context.WithCancel(context.Background())
	cancelP()
	if s, _, ok := ContextOutcome(plain); !ok || s != StateCancelled {
		t.Errorf("plain cancel state = %q, want Cancelled", s)
	}
}

// cancelRunner simulates an external cancel arriving mid-run.
type cancelRunner struct{ cancel context.CancelFunc }

func (r cancelRunner) Run(ctx context.Context, spec runner.Spec) (runner.Result, error) {
	r.cancel()
	return runner.Result{}, ctx.Err()
}

// An explicit cancel (context.Canceled, not a deadline) yields Cancelled, not
// Failed (§7.4).
func TestRunCancelledIsCancelled(t *testing.T) {
	origin := initOrigin(t)
	ctx, cancel := context.WithCancel(context.Background())
	out, err := Run(ctx,
		Deps{Repo: repo.NewManager(t.TempDir()), Runner: cancelRunner{cancel}, Image: "x"},
		Request{
			TaskID: "c", Title: "x", RepoName: "agent-task",
			RepoURL: "file://" + origin, DefaultBranch: "main",
			Prompt: prompt.Input{Task: "t"}, Agent: agent.Mock(), AuthMethod: agent.AuthAPIKey,
			WorkDir: t.TempDir(),
		})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.State != StateCancelled {
		t.Errorf("state = %q, want Cancelled", out.State)
	}
	if out.Error != "cancelled" {
		t.Errorf("error = %q, want cancelled", out.Error)
	}
}
