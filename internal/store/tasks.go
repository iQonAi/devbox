package store

import (
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
