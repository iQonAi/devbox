package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/iQonAi/agent-task/internal/api"
	"github.com/iQonAi/agent-task/internal/config"
	"github.com/iQonAi/agent-task/internal/controller"
	"github.com/iQonAi/agent-task/internal/pool"
	"github.com/iQonAi/agent-task/internal/store"
)

func TestSubmitCreatesTaskAndEnqueues(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "") // no secrets in the test

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	got := make(chan controller.Request, 1)
	p := pool.New(context.Background(), func(_ context.Context, req controller.Request) (controller.Outcome, error) {
		got <- req
		return controller.Outcome{}, nil
	}, 1, 4)

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Limits:  config.Limits{MaxConcurrent: 1, TaskTimeout: "30m"},
		Repos:   []config.Repo{{Name: "agent-task", Owner: "iQonAi", Repo: "agent-task", DefaultBranch: "main", TokenRef: "gh"}},
		Agents:  map[string]config.AgentConfig{"mock": {Auth: "api_key", TokenRef: "model"}},
	}
	s := &submitter{cfg: cfg, store: st, pool: p}

	id, err := s.Submit(api.SubmitRequest{Repo: "agent-task", Agent: "mock", Task: "do a thing"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := <-got
	if req.TaskID != id || req.RepoName != "agent-task" || req.Title != "do a thing" {
		t.Errorf("enqueued req = %+v", req)
	}
	tasks, _ := st.ListTasks()
	if len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("task not persisted: %+v", tasks)
	}

	if _, err := s.Submit(api.SubmitRequest{Repo: "nope", Agent: "mock", Task: "x"}); err == nil {
		t.Error("expected error for unknown repo")
	}
	if _, err := s.Submit(api.SubmitRequest{Repo: "agent-task", Agent: "ghost", Task: "x"}); err == nil {
		t.Error("expected error for unconfigured agent")
	}
}

// fakeKiller records which tasks had their containers destroyed and can
// simulate a podman failure.
type fakeKiller struct {
	destroyed []string
	err       error
}

func (f *fakeKiller) Destroy(_ context.Context, taskID string) error {
	f.destroyed = append(f.destroyed, taskID)
	return f.err
}

func TestRecoverInflight(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.UpsertRepo(store.Repo{Name: "r", Owner: "o", Repo: "r", DefaultBranch: "main", TokenRef: "t"}); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.ListRepos()
	id := repos[0].ID

	mk := func(tid, state string) {
		if err := st.CreateTask(store.NewTask{ID: tid, RepoID: id, Source: "task"}); err != nil {
			t.Fatal(err)
		}
		if state != store.StateCreated {
			_ = st.UpdateTaskState(tid, state)
		}
	}
	mk("running", store.StateRunning)
	mk("created", store.StateCreated)
	mk("done", store.StateCompleted)

	killer := &fakeKiller{}
	if n := recoverInflight(context.Background(), st, killer); n != 2 {
		t.Errorf("recovered %d, want 2 (running + created)", n)
	}
	tasks, _ := st.ListTasks()
	for _, task := range tasks {
		if task.ID == "done" && task.State != store.StateCompleted {
			t.Errorf("terminal task was touched: %s", task.State)
		}
		if (task.ID == "running" || task.ID == "created") && task.State != store.StateFailed {
			t.Errorf("%s not failed: %s", task.ID, task.State)
		}
	}

	// Orphaned containers are destroyed for both recovered tasks — and only
	// those — and the destruction is audited.
	if len(killer.destroyed) != 2 {
		t.Fatalf("destroyed %v, want the 2 recovered tasks", killer.destroyed)
	}
	for _, tid := range killer.destroyed {
		if tid != "running" && tid != "created" {
			t.Errorf("destroyed container for non-recovered task %q", tid)
		}
		events, _ := st.ListEvents(tid)
		found := false
		for _, e := range events {
			if e.Type == store.EventSecurity && e.Message == "container destroyed (recovery)" {
				found = true
			}
		}
		if !found {
			t.Errorf("no destroy event for %q: %+v", tid, events)
		}
	}
}

// A podman failure during recovery is best-effort: still recovered, no
// destroy event claimed.
func TestRecoverInflightDestroyFailure(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.UpsertRepo(store.Repo{Name: "r", Owner: "o", Repo: "r", DefaultBranch: "main", TokenRef: "t"}); err != nil {
		t.Fatal(err)
	}
	repos, _ := st.ListRepos()
	if err := st.CreateTask(store.NewTask{ID: "stuck", RepoID: repos[0].ID, Source: "task"}); err != nil {
		t.Fatal(err)
	}
	_ = st.UpdateTaskState("stuck", store.StateRunning)

	killer := &fakeKiller{err: fmt.Errorf("podman unavailable")}
	if n := recoverInflight(context.Background(), st, killer); n != 1 {
		t.Errorf("recovered %d, want 1", n)
	}
	tasks, _ := st.ListTasks()
	if tasks[0].State != store.StateFailed {
		t.Errorf("state = %q, want Failed", tasks[0].State)
	}
	events, _ := st.ListEvents("stuck")
	for _, e := range events {
		if e.Message == "container destroyed (recovery)" {
			t.Error("destroy event recorded although rm failed")
		}
	}
}

// Cancelling a task that is still queued records Created->Cancelled: nothing
// else will, since the pool never runs it.
func TestCancelQueuedRecordsCancelled(t *testing.T) {
	t.Setenv("CREDENTIALS_DIRECTORY", "")

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// One worker, held busy by the first task so the second stays queued.
	started := make(chan string, 1)
	release := make(chan struct{})
	p := pool.New(context.Background(), func(_ context.Context, req controller.Request) (controller.Outcome, error) {
		started <- req.TaskID
		<-release
		return controller.Outcome{}, nil
	}, 1, 4)
	defer close(release)

	cfg := &config.Config{
		DataDir: t.TempDir(),
		Limits:  config.Limits{MaxConcurrent: 1, TaskTimeout: "30m"},
		Repos:   []config.Repo{{Name: "agent-task", Owner: "iQonAi", Repo: "agent-task", DefaultBranch: "main", TokenRef: "gh"}},
		Agents:  map[string]config.AgentConfig{"mock": {Auth: "api_key", TokenRef: "model"}},
	}
	s := &submitter{cfg: cfg, store: st, pool: p}

	if _, err := s.Submit(api.SubmitRequest{Repo: "agent-task", Agent: "mock", Task: "hold the worker"}); err != nil {
		t.Fatalf("submit 1: %v", err)
	}
	<-started
	queuedID, err := s.Submit(api.SubmitRequest{Repo: "agent-task", Agent: "mock", Task: "stay queued"})
	if err != nil {
		t.Fatalf("submit 2: %v", err)
	}

	if err := s.Cancel(queuedID); err != nil {
		t.Fatalf("cancel queued: %v", err)
	}

	tasks, _ := st.ListTasks()
	for _, task := range tasks {
		if task.ID == queuedID && task.State != store.StateCancelled {
			t.Errorf("queued task state = %q, want Cancelled", task.State)
		}
	}
	events, _ := st.ListEvents(queuedID)
	found := false
	for _, e := range events {
		if e.Type == store.EventState && e.Message == "Created->Cancelled: cancelled before start" {
			found = true
		}
	}
	if !found {
		t.Errorf("no Created->Cancelled event for %s: %+v", queuedID, events)
	}
}
