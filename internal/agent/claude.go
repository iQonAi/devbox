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
	return fmt.Sprintf(
		`claude -p "$(cat %s)" --dangerously-skip-permissions --output-format json > %s`,
		promptPath, transcriptPath,
	), nil
}
