package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCommander records calls and can simulate an exit code or a failure.
type fakeCommander struct {
	calls    [][]string
	exitCode int    // returned for "start"
	failOn   string // if any arg contains this substring, run returns an error
}

func (f *fakeCommander) run(_ context.Context, args ...string) (string, int, error) {
	f.calls = append(f.calls, args)
	if f.failOn != "" {
		for _, a := range args {
			if strings.Contains(a, f.failOn) {
				return "", -1, &simErr{f.failOn}
			}
		}
	}
	if len(args) > 0 && args[0] == "start" {
		return "", f.exitCode, nil
	}
	return "", 0, nil
}

type simErr struct{ on string }

func (e *simErr) Error() string { return "simulated failure: " + e.on }

// calledWith reports whether any recorded call contains all the given tokens.
func (f *fakeCommander) calledWith(tokens ...string) bool {
	for _, call := range f.calls {
		joined := strings.Join(call, " ")
		all := true
		for _, tok := range tokens {
			if !strings.Contains(joined, tok) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func newSpec(t *testing.T) Spec {
	return Spec{
		Name: "task-1", Image: "img", Cmd: []string{"true"},
		SourceDir: t.TempDir(), OutDir: filepath.Join(t.TempDir(), "out"),
	}
}

func TestRunHappyPath(t *testing.T) {
	f := &fakeCommander{exitCode: 0}
	r := &PodmanRunner{cmd: f}

	res, err := r.Run(context.Background(), newSpec(t))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit = %d, want 0", res.ExitCode)
	}
	// The full lifecycle, in the proven order.
	for _, step := range [][]string{
		{"volume", "create", "agent-task-task-1"},
		{"create", "--name", "agent-task-task-1"},
		{"cp", "/.", "agent-task-task-1:" + SrcPath},
		{"start", "-a", "agent-task-task-1"},
		{"cp", "agent-task-task-1:" + OutPath},
	} {
		if !f.calledWith(step...) {
			t.Errorf("missing lifecycle step: %v", step)
		}
	}
}

func TestRunPropagatesExitCode(t *testing.T) {
	f := &fakeCommander{exitCode: 7}
	res, err := (&PodmanRunner{cmd: f}).Run(context.Background(), newSpec(t))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit = %d, want 7 (non-zero container exit is not a runner error)", res.ExitCode)
	}
}

func TestRunTearsDownOnFailure(t *testing.T) {
	f := &fakeCommander{failOn: "start"}
	_, err := (&PodmanRunner{cmd: f}).Run(context.Background(), newSpec(t))
	if err == nil {
		t.Fatal("expected an error when start fails")
	}
	// Teardown must still have run.
	if !f.calledWith("rm", "-f", "agent-task-task-1") {
		t.Error("container not torn down after failure")
	}
	if !f.calledWith("volume", "rm", "-f", "agent-task-task-1") {
		t.Error("volume not torn down after failure")
	}
}

// An unsafe or empty name must be rejected before any podman call, since it
// gets embedded into container names and `podman cp` targets.
func TestRunRejectsBadName(t *testing.T) {
	for _, bad := range []string{"", "has space", "a:b", "a/b", "-leading"} {
		f := &fakeCommander{}
		spec := newSpec(t)
		spec.Name = bad
		if _, err := (&PodmanRunner{cmd: f}).Run(context.Background(), spec); err == nil {
			t.Errorf("name %q was accepted, want rejection", bad)
		}
		if len(f.calls) != 0 {
			t.Errorf("name %q: podman invoked before validation", bad)
		}
	}
}

// A copy-in failure must abort before the container is ever started.
func TestRunAbortsIfCopyInFails(t *testing.T) {
	f := &fakeCommander{failOn: SrcPath}
	_, err := (&PodmanRunner{cmd: f}).Run(context.Background(), newSpec(t))
	if err == nil || !strings.Contains(err.Error(), "copy source in") {
		t.Fatalf("want copy-in error, got %v", err)
	}
	if f.calledWith("start", "-a") {
		t.Error("container started despite copy-in failure")
	}
}
