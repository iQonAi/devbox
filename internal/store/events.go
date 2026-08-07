package store

import (
	"fmt"
	"time"
)

// event types - the controlled vocabulary for the append-only audit trail
const (
	EventState    = "state"    // a lifecycle transition (Created->Running->Completed)
	EventPhase    = "phase"    // an internal phase within Running
	EventSecurity = "security" // a security-relevant action (container launch / destroy, push, PR, cancel)
)

// Event is one row in the append-only task_events audit trail.
type Event struct {
	ID      int64     `json:"id"`
	TaskID  string    `json:"task_id"`
	TS      time.Time `json:"ts"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}

// InsertEvent appends one audit event. Events are write-once - never update or
// deleted - so the trail is a faithful record of what happened.
func (s *Store) InsertEvent(taskID, eventType, message string) error {
	if _, err := s.db.Exec(
		`INSERT INTO task_events (task_id, ts, type, message) VALUES (?, ?, ?, ?)`,
		taskID, time.Now().UTC(), eventType, message,
	); err != nil {
		return fmt.Errorf("insert %s event for %q: %w", eventType, taskID, err)
	}
	return nil
}

// ListEvents returns a task's events in chronological (insertion) order. Ordering
// by the monotonic id, not ts, keeps the order stable even if two events share a
// timestamp.
func (s *Store) ListEvents(taskID string) ([]Event, error) {
	rows, err := s.db.Query(
		`SELECT id, task_id, ts, type, message FROM task_events WHERE task_id = ? ORDER BY id`, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TaskID, &e.TS, &e.Type, &e.Message); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	return events, nil
}
