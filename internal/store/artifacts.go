package store

import "fmt"

// Artifact is a file produced by a task run (bundle, transcript, diff, summary,
// log), recorded so the host can index what a run left behind.
type Artifact struct {
	ID     int64  `json:"id"`
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
}

// InsertArtifact records one artifact for a task.
func (s *Store) InsertArtifact(taskID, kind, path string) error {
	if _, err := s.db.Exec(
		`INSERT INTO artifacts (task_id, kind, path) VALUES (?, ?, ?)`,
		taskID, kind, path,
	); err != nil {
		return fmt.Errorf("insert %s artifact for %q: %w", kind, taskID, err)
	}
	return nil
}

// ListArtifacts returns a task's artifacts in insertion order.
func (s *Store) ListArtifacts(taskID string) ([]Artifact, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, kind, path FROM artifacts WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()

	var arts []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Kind, &a.Path); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		arts = append(arts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return arts, nil
}
