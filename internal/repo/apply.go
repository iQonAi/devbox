package repo

import (
	"context"
	"fmt"
	"strconv"

	"github.com/iQonAi/agent-task/internal/gitx"
)

// ApplyResult reports what a bundle apply landed on the feature branch.
type ApplyResult struct {
	Commits int    // number of commits applied
	Head    string // new branch tip after the apply
}

// ApplyBundle fetches the agent's commits from bundlePath and fast-forwards the
// feature branch checked out in worktreeDir onto them. The bundle is inert data
// (§8.3): git reads it, the host never executes anything from it. The worktree
// sits at the base commit and the bundle contains base..agentHEAD, so a
// fast-forward is always valid; a non-fast-forward means the bundle does not
// descend from base and is rejected rather than force-applied.
func (m *Manager) ApplyBundle(ctx context.Context, worktreeDir, bundlePath string) (ApplyResult, error) {
	before, err := gitx.Run(ctx, worktreeDir, "rev-parse", "HEAD")
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read branch head: %w", err)
	}

	// Verify the bundle is well-formed and its prerequisites are present before
	// touching the branch.
	if _, err := gitx.Run(ctx, worktreeDir, "bundle", "verify", bundlePath); err != nil {
		return ApplyResult{}, fmt.Errorf("verify bundle: %w", err)
	}
	if _, err := gitx.Run(ctx, worktreeDir, "fetch", bundlePath, "HEAD"); err != nil {
		return ApplyResult{}, fmt.Errorf("fetch bundle: %w", err)
	}
	if _, err := gitx.Run(ctx, worktreeDir, "merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return ApplyResult{}, fmt.Errorf("apply bundle (fast-forward only): %w", err)
	}

	after, err := gitx.Run(ctx, worktreeDir, "rev-parse", "HEAD")
	if err != nil {
		return ApplyResult{}, fmt.Errorf("read new head: %w", err)
	}
	countStr, err := gitx.Run(ctx, worktreeDir, "rev-list", "--count", before+".."+after)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("count applied commits: %w", err)
	}
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("parse commit count %q: %w", countStr, err)
	}
	return ApplyResult{Commits: count, Head: after}, nil
}
