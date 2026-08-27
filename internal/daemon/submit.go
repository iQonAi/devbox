package daemon

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iQonAi/agent-task/internal/agent"
	"github.com/iQonAi/agent-task/internal/api"
	"github.com/iQonAi/agent-task/internal/config"
	"github.com/iQonAi/agent-task/internal/controller"
	"github.com/iQonAi/agent-task/internal/creds"
	"github.com/iQonAi/agent-task/internal/pool"
	"github.com/iQonAi/agent-task/internal/prompt"
	"github.com/iQonAi/agent-task/internal/store"
)

// submitter builds a controller.Request from an API submission — resolving
// secrets host-side via LoadCredential (D3) — persists the task, and enqueues
// it on the pool. It implements api.Submitter.
type submitter struct {
	cfg   *config.Config
	store *store.Store
	pool  *pool.Pool
}

func (s *submitter) Submit(sr api.SubmitRequest) (string, error) {
	if sr.Task == "" && sr.Issue == 0 {
		return "", fmt.Errorf("one of task or issue is required")
	}
	if sr.Task != "" && sr.Issue != 0 {
		return "", fmt.Errorf("task and issue are mutually exclusive")
	}

	var rc *config.Repo
	for i := range s.cfg.Repos {
		if s.cfg.Repos[i].Name == sr.Repo {
			rc = &s.cfg.Repos[i]
			break
		}
	}
	if rc == nil {
		return "", fmt.Errorf("unknown repo %q", sr.Repo)
	}

	ag, err := agent.Lookup(sr.Agent)
	if err != nil {
		return "", err
	}
	ac, ok := s.cfg.Agents[sr.Agent]
	if !ok {
		return "", fmt.Errorf("agent %q not configured", sr.Agent)
	}

	// Resolve secrets from LoadCredential (host-only). Absent = empty (anon clone
	// / no model auth), which the downstream steps surface as a clear failure.
	ghToken, _, err := creds.Get(rc.TokenRef)
	if err != nil {
		return "", err
	}
	modelToken, _, err := creds.Get(ac.TokenRef)
	if err != nil {
		return "", err
	}

	if err := s.store.UpsertRepo(store.Repo{
		Name: rc.Name, Owner: rc.Owner, Repo: rc.Repo,
		DefaultBranch: rc.DefaultBranch, TokenRef: rc.TokenRef,
	}); err != nil {
		return "", err
	}
	repoID, err := s.repoID(rc.Name)
	if err != nil {
		return "", err
	}

	// UnixNano alone can collide (clock steps, parallel submits); a random
	// suffix makes the id unique. Hex keeps it inside the container-name
	// charset ([a-z0-9-], see runner's name validation).
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("task id suffix: %w", err)
	}
	taskID := fmt.Sprintf("t%d-%x", time.Now().UnixNano(), suffix)
	source := "task"
	if sr.Issue > 0 {
		source = "issue"
	}
	if err := s.store.CreateTask(store.NewTask{
		ID: taskID, RepoID: repoID, Source: source, Agent: sr.Agent,
	}); err != nil {
		return "", err
	}
	_ = s.store.InsertEvent(taskID, store.EventState, "Created")

	// Per-task scratch under the shared work root (agentbox-accessible; the root
	// is setgid to a shared group so both the daemon uid and the container uid
	// reach it). 2770 + setgid propagates the group to podman-written files, so
	// the daemon can read back what agentbox wrote. Falls back to data_dir/work
	// for local/test runs where the two uids are the same.
	workRoot := s.cfg.WorkDir
	if workRoot == "" {
		workRoot = filepath.Join(s.cfg.DataDir, "work")
	}
	workDir := filepath.Join(workRoot, taskID)
	for _, d := range []string{workDir, filepath.Join(workDir, "out")} {
		if err := os.MkdirAll(d, 0o770); err != nil {
			return "", err
		}
		if err := os.Chmod(d, os.ModeSetgid|0o770); err != nil {
			return "", err
		}
	}

	timeout, _ := time.ParseDuration(s.cfg.Limits.TaskTimeout)
	req := controller.Request{
		TaskID:   taskID,
		Title:    sr.Task,
		RepoName: rc.Name,
		RepoURL:  fmt.Sprintf("https://github.com/%s/%s.git", rc.Owner, rc.Repo),
		Owner:    rc.Owner, Repo: rc.Repo, DefaultBranch: rc.DefaultBranch,
		IssueNumber: sr.Issue, GitHubToken: ghToken,
		Prompt: prompt.Input{Task: sr.Task},
		Agent:  ag, AuthMethod: agent.AuthMethod(ac.Auth), AuthValue: modelToken,
		WorkDir: workDir,
		Limits:  controller.Limits{CPUs: "2", MemoryMB: 2048, PidsLimit: 256, Timeout: timeout},
	}
	if err := s.pool.Submit(req); err != nil {
		_ = s.store.UpdateTaskState(taskID, store.StateFailed)
		_ = s.store.InsertEvent(taskID, store.EventState, "->Failed: "+err.Error())
		return "", err
	}
	return taskID, nil
}

func (s *submitter) Cancel(taskID string) error {
	found, queued := s.pool.Cancel(taskID)
	if !found {
		return fmt.Errorf("task %q is not running", taskID)
	}
	_ = s.store.InsertEvent(taskID, store.EventSecurity, "cancel requested")
	if queued {
		// The task never started (the pool skips it), so nothing else will
		// record its terminal state — do it here (§7.1: cancel before start).
		_ = s.store.UpdateTaskState(taskID, store.StateCancelled)
		_ = s.store.InsertEvent(taskID, store.EventState, "Created->Cancelled: cancelled before start")
	}
	return nil
}

func (s *submitter) repoID(name string) (int64, error) {
	repos, err := s.store.ListRepos()
	if err != nil {
		return 0, err
	}
	for _, r := range repos {
		if r.Name == name {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("repo %q not found after upsert", name)
}
