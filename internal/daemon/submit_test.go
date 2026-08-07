package daemon

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/iQonAi/devbox/internal/api"
	"github.com/iQonAi/devbox/internal/config"
	"github.com/iQonAi/devbox/internal/controller"
	"github.com/iQonAi/devbox/internal/pool"
	"github.com/iQonAi/devbox/internal/store"
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
		Repos:   []config.Repo{{Name: "devbox", Owner: "iQonAi", Repo: "devbox", DefaultBranch: "main", TokenRef: "gh"}},
		Agents:  map[string]config.AgentConfig{"mock": {Auth: "api_key", TokenRef: "model"}},
	}
	s := &submitter{cfg: cfg, store: st, pool: p}

	id, err := s.Submit(api.SubmitRequest{Repo: "devbox", Agent: "mock", Task: "do a thing"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	req := <-got
	if req.TaskID != id || req.RepoName != "devbox" || req.Title != "do a thing" {
		t.Errorf("enqueued req = %+v", req)
	}
	tasks, _ := st.ListTasks()
	if len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("task not persisted: %+v", tasks)
	}

	if _, err := s.Submit(api.SubmitRequest{Repo: "nope", Agent: "mock", Task: "x"}); err == nil {
		t.Error("expected error for unknown repo")
	}
	if _, err := s.Submit(api.SubmitRequest{Repo: "devbox", Agent: "ghost", Task: "x"}); err == nil {
		t.Error("expected error for unconfigured agent")
	}
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

	if n := recoverInflight(st); n != 2 {
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
}
