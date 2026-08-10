package agent

import "fmt"

// claude is the Claude Code adapter (M3).
type claude struct{}

// Claude returns the Claude Code adapter.
func Claude() Agent { return claude{} }

func (claude) Name() string { return "claude" }

func (claude) EnvVar(method AuthMethod) (string, error) {
	switch method {
	case AuthSubscription:
		return "CLAUDE_CODE_OAUTH_TOKEN", nil
	case AuthAPIKey:
		return "ANTHROPIC_API_KEY", nil
	default:
		return "", fmt.Errorf("claude: unsupported auth method %q", method)
	}
}

func (c claude) Command(method AuthMethod, promptPath, transcriptPath string) (string, error) {
	if _, err := c.EnvVar(method); err != nil {
		return "", err
	}
	// Notes:
	//   --dangerously-skip-permissions: the disposable, non-root, egress-restricted
	//     container is the security boundary; Claude's host-protection prompts are
	//     redundant here and there is no TTY to answer them.
	//   No --bare: it ignores CLAUDE_CODE_OAUTH_TOKEN, which subscription auth needs.
	//   --output-format json: machine-readable transcript (result + cost + tokens).
	//   Summary: claude's transcript is a single JSON object whose .result holds
	//     the final assistant text — the PR summary. Extraction is best-effort
	//     (`|| true`, stderr dropped) and runs even when claude fails; the
	//     captured status is re-raised so the summary step never masks the
	//     agent's exit code.
	return fmt.Sprintf(
		`claude -p "$(cat %[1]s)" --dangerously-skip-permissions --output-format json > %[2]s; `+
			`AGENT_STATUS=$?; `+
			`jq -r '.result // empty' %[2]s > %[3]s 2>/dev/null || true; `+
			`(exit "$AGENT_STATUS")`,
		promptPath, transcriptPath, summaryPath(transcriptPath),
	), nil
}
