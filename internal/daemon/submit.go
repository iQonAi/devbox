package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iQonAi/devbox/internal/agent"
	"github.com/iQonAi/devbox/internal/api"
	"github.com/iQonAi/devbox/internal/config"
	"github.com/iQonAi/devbox/internal/controller"
	"github.com/iQonAi/devbox/internal/creds"
	"github.com/iQonAi/devbox/internal/pool"
	"github.com/iQonAi/devbox/internal/prompt"
	"github.com/iQonAi/devbox/internal/store"
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

	taskID := fmt.Sprintf("t%d", time.Now().UnixNano())
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

	// Per-task scratch, agentbox-accessible (M5 hardens to a shared group in the
	// deploy step; single-user host makes world-access acceptable meanwhile).
	workDir := filepath.Join(s.cfg.DataDir, "work", taskID)
	for _, d := range []string{workDir, filepath.Join(workDir, "out")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			return "", err
		}
		if err := os.Chmod(d, 0o777); err != nil {
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
	if !s.pool.Cancel(taskID) {
		return fmt.Errorf("task %q is not running", taskID)
	}
	_ = s.store.InsertEvent(taskID, store.EventSecurity, "cancel requested")
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
