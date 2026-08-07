package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Task is a row in the tasks table. M0 only reads; task creation lands in M1
// so we select just the always-present columns for now.
type Task struct {
	ID        string    `json:"id"`
	RepoID    int64     `json:"repo_id"`
	Source    string    `json:"source"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// Task states, stored verbatim as in the design ("0002", verbatim)
// Created and Running are live; the rest are terminal.
const (
	StateCreated   = "Created"
	StateRunning   = "Running"
	StateCompleted = "Completed"
	StateFailed    = "Failed"
	StateCancelled = "Cancelled"
)

// NewTask is the input for creating a task, Nullable lifecycle columns
// (exit_code, pr_url, ...) are filled in later as the task progresses.
type NewTask struct {
	ID           string
	RepoID       int64
	Source       string
	Agent        string
	Branch       string
	HostWorktree string
}

// CreateTask inserts a task in the Created state.
func (s *Store) CreateTask(t NewTask) error {
	_, err := s.db.Exec(
		`
			INSERT INTO tasks (id, repo_id, source, agent, branch, host_worktree, state, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.RepoID, t.Source, t.Agent, t.Branch, t.HostWorktree, StateCreated, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("created test %q: %w", t.ID, err)
	}
	return nil
}

// UpdateTaskState moves a task to a new state
func (s *Store) UpdateTaskState(id, state string) error {
	res, err := s.db.Exec(`UPDATE tasks SET state = ? WHERE id = ?`, state, id)
	if err != nil {
		return fmt.Errorf("update task %q state: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no task with id %q", id)
	}
	return nil
}

// SetTaskBranchWorktree records a task's feature branch and host worktree path
// as soon as they exist, so the orphan sweep can reclaim the worktree once the
// task reaches a terminal state.
func (s *Store) SetTaskBranchWorktree(id, branch, worktree string) error {
	res, err := s.db.Exec(`UPDATE tasks SET branch = ?, host_worktree = ? WHERE id = ?`, branch, worktree, id)
	if err != nil {
		return fmt.Errorf("set branch/worktree for %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no task with id %q", id)
	}
	return nil
}

// ListTasks returns all tasks, newest first.  Empty at M0 (nothing creates tasks yet).
func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.db.Query(`
					SELECT id, repo_id, source, state, created_at
					FROM tasks
					ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.RepoID, &t.Source, &t.State, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan tasks: %w", err)
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return tasks, nil
}

// SweepableTask is a terminal-state task whose host worktree may still exist on
// disk - the orphan sweep's work list.
type SweepableTask struct {
	ID           string
	Branch       string
	HostWorktree string
	MirrorPath   string
}

// SweepableTasks returns terminal-state tasks that still carry a worktree path,
// joined to their repo's mirror so the sweeper can act on them.
func (s *Store) SweepableTasks() ([]SweepableTask, error) {
	rows, err := s.db.Query(
		`
		  SELECT t.id, t.branch, t.host_worktree, r.mirror_path
              FROM tasks t
              JOIN repos r ON r.id = t.repo_id
              WHERE t.state IN (?, ?, ?)
                AND t.host_worktree IS NOT NULL
                AND t.host_worktree != ''`,
		StateCompleted, StateFailed, StateCancelled,
	)
	if err != nil {
		return nil, fmt.Errorf("query sweepable tasks: %w", err)
	}
	defer rows.Close()

	var tasks []SweepableTask
	for rows.Next() {
		var t SweepableTask

		// branch and mirror_path are nullable columns; a plain string cannot
		// hold NULL, so scan into sql.NullString and read the .String field
		// (which is "" when the column was NULL).
		var branch, mirror sql.NullString
		if err := rows.Scan(&t.ID, &branch, &t.HostWorktree, &mirror); err != nil {
			return nil, fmt.Errorf("scan sweepable task: %w", err)
		}
		t.Branch = branch.String
		t.MirrorPath = mirror.String
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sweepable tasks: %w", err)
	}
	return tasks, nil
}

// ClearTaskWorktree forgets a task's worktree path once the worktree is gone,
// so the startup sweep does not process it again. Idempotent by design: a
// no-op on an unknown id is fine, so now RowsAffected check.
func (s *Store) ClearTaskWorktree(id string) error {
	if _, err := s.db.Exec(`UPDATE tasks SET host_worktree = NULL WHERE id = ?`, id); err != nil {
		return fmt.Errorf("clear worktree for %q: %w", id, err)
	}
	return nil
}

// SetTaskPRURL records the pull request opened for a task (M4 back-link).
func (s *Store) SetTaskPRURL(id, url string) error {
	res, err := s.db.Exec(`UPDATE tasks SET pr_url = ? WHERE id = ?`, url, id)
	if err != nil {
		return fmt.Errorf("set pr_url for %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("no task with id %q", id)
	}
	return nil
}
