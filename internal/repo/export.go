package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iQonAi/agent-task/internal/gitx"
)

// BuildExport creates a self-contained clone of srcRepo at branch into destDir:
// a standalone git repo with its own object store and NO reference back to
// srcRepo. This is the copy handed to the container (§8.4). --no-hardlinks
// forces a real copy so the container shares no inodes with the host mirror,
// and --single-branch keeps it small. The clone carries no remote pointing at
// anything the container could reach.
func (m *Manager) BuildExport(ctx context.Context, srcRepo, branch, destDir string) error {
	if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
		return fmt.Errorf("create export parent: %w", err)
	}
	if _, err := gitx.Run(ctx, filepath.Dir(destDir),
		"clone", "--no-hardlinks", "--single-branch", "--branch", branch, srcRepo, destDir,
	); err != nil {
		return fmt.Errorf("build export of %s@%s: %w", srcRepo, branch, err)
	}
	// Drop the origin remote so the container copy is truly standalone — no URL
	// for a hostile agent to fetch from or push to.
	if _, err := gitx.Run(ctx, destDir, "remote", "remove", "origin"); err != nil {
		return fmt.Errorf("detach export remote: %w", err)
	}
	return nil
}

// ExportBase returns the commit the export/worktree started from — the tip of
// branch in the export. The container bundles base..HEAD, and the host applies
// that onto the feature branch.
func (m *Manager) ExportBase(ctx context.Context, dir string) (string, error) {
	rev, err := gitx.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read export base: %w", err)
	}
	return rev, nil
}
