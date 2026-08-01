package controller

import (
	"fmt"

	"github.com/iQonAi/devbox/internal/runner"
)

// wrapperCmd builds the container command: it sets a git identity, runs the
// agent, then — agent-agnostic — bundles the agent's commits (base..HEAD),
// captures the diff, extracts a summary, and exits with the agent's status.
// `set +e` ensures a failing agent still gets its artifacts collected. base is a
// commit SHA (not a secret); the agent command comes from the adapter.
func wrapperCmd(base, agentCmd string) []string {
	script := fmt.Sprintf(`set +e
cd %[1]s
exec 2> %[2]s/run.log
git config user.email "agent@localhost"
git config user.name "agent-task"
%[3]s
AGENT_EXIT=$?
if [ "$(git rev-list %[4]s..HEAD --count 2>/dev/null || echo 0)" -gt 0 ]; then
  git bundle create %[2]s/changes.bundle %[4]s..HEAD
  git diff %[4]s HEAD > %[2]s/diff.patch
fi
if command -v jq >/dev/null 2>&1; then
  jq -r '.result // empty' %[2]s/transcript.json > %[2]s/summary.txt 2>/dev/null || true
fi
printf '%%s\n' "$AGENT_EXIT" > %[2]s/agent.exit
exit "$AGENT_EXIT"
`, runner.SrcPath, runner.OutPath, agentCmd, base)
	// bash -c, NOT -lc: a login shell sources /etc/profile and ~/.profile, but
	// the container runs with HOME=/task (no profile there), so -l resets PATH
	// and drops /home/agent/.local/bin where the agent CLIs live. -c inherits
	// the image's ENV PATH intact.
	return []string{"bash", "-c", script}
}
