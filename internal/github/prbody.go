package github

import (
	"fmt"
	"strings"
)

// PRInfo is the data rendered into a templated PR body.
type PRInfo struct {
	TaskID     string
	Agent      string
	IssueURL   string // "" for a --task run
	Summary    string // the agent's summary (from the run's summary artifact)
	TestOutput string // optional captured test output; "" to omit
}

// BuildPRBody renders the deterministic PR body. It ends with a plain,
// no-emoji safety marker: the PR is the only human approval gate (0001), so a
// reviewer must know the change is agent-authored and unmerged.
func BuildPRBody(info PRInfo) string {
	var b strings.Builder
	b.WriteString("## Agent task\n\n")
	fmt.Fprintf(&b, "- Task: `%s`\n", info.TaskID)
	fmt.Fprintf(&b, "- Agent: `%s`\n", info.Agent)
	if info.IssueURL != "" {
		fmt.Fprintf(&b, "- Issue: %s\n", info.IssueURL)
	}

	b.WriteString("\n## Summary\n\n")
	if s := strings.TrimSpace(info.Summary); s != "" {
		b.WriteString(s)
	} else {
		b.WriteString("_(no summary produced)_")
	}
	b.WriteString("\n")

	if t := strings.TrimSpace(info.TestOutput); t != "" {
		b.WriteString("\n## Test results\n\n```\n")
		b.WriteString(t)
		b.WriteString("\n```\n")
	}

	b.WriteString("\n---\n\n")
	b.WriteString("**Agent-produced — human review required. Do not merge without review.**\n")
	return b.String()
}
