package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Identity used for any commit git creates host-side. Ambien config is
// disabled, so without this git would fail with "empty ident name".
const (
	authorName  = "devbox"
	authorEmail = "devbox@localhost"
)

// Run executes git in dir and returns trimmed stdout.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	return RunEnv(ctx, dir, nil, args...)
}

// RunEnv is Run with extra environment entries ("KEY=value"), used to hand a
// credential helper a token wihtout ever putting it arg - /proc/<pid>/cmdline
// is world-readable, so a token in an argument is a token leaked to every user
// on the host
func RunEnv(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	// -c flags come before the subcommand. gc.auto=0 stops git from forking a
	// background "gc --auto" that would mutate these ephemeral mirrors/worktrees
	// mid-task (and outlive short-lived operations, racing their cleanup).
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "gc.auto=0"}, args...)

	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(baseEnv(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// args never carry secrets (see RunEnv doc), so they are safe to echo.
		return "", fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// baseEnv builds the environment from scratch rather than extending the
// daemon's, so nothing ambient (GIT_*, SSH_ASKPASS, a credential cache) can
// influence a run.
func baseEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		// No user config and no credential store, wherever this runs.
		"HOME=/nonexistent",
		// Ignore /etct/gitconfig and ~/.gitconfig
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		// Never block on a credenital prompt: fail loudly instead of hanging
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + authorName,
		"GIT_COMMITTER_EMAIL=" + authorEmail,
	}
}
