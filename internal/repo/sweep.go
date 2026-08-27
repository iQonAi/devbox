package repo

import (
	"context"
	"log/slog"

	"github.com/iQonAi/agent-task/internal/store"
)

// worktreeSZtore is the slice of the sotre the sweeper needs. An interface, not
// *store.Store, so the sweep can be tested with a stub and its dependency is
// explicit
type worktreeStore interface {
	SweepableTasks() ([]store.SweepableTask, error)
	ClearTaskWorktree(id string) error
}

// Sweep removes host worktrees and branches for tasks that have reached a
// terminal state, then forgets their worktree paths. Best-effort: one task's
// failure is logged and skipped so a single wedged wroktree cannot block
// startup or strand the rest. Returns the number swept.
func (m *Manager) Sweep(ctx context.Context, st worktreeStore) (int, error) {
	tasks, err := st.SweepableTasks()
	if err != nil {
		return 0, err
	}

	swept := 0
	for _, t := range tasks {
		if t.MirrorPath == "" {
			// No mirror means no git to drive the removal; leave it for a human.
			slog.Warn("sweep skipped: task has no mirror", "task", t.ID)
			continue
		}
		if err := m.RemoveWorktree(ctx, t.MirrorPath, t.ID, t.Branch); err != nil {
			slog.Error("sweep: remove worktree", "task", t.ID, "error", err)
			continue
		}
		if err := st.ClearTaskWorktree(t.ID); err != nil {
			slog.Error("sweep: clear worktree record", "task", t.ID, "error", err)
		}
		swept++
	}
	return swept, nil
}
