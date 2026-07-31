// Package prompt renders a task into the deterministic prompt artifact handed to
// the agent (read-only at /task/prompt.md). Deterministic: the same input always
// produces byte-identical output, so a run is reproducible and testable — no
// timestamps, no ordering surprises.
package prompt

import (
	"fmt"
	"strings"
)

// Input is a task to render. Exactly one of Task or Issue must be set (D8:
// --task free-form text, or --issue fetched into an Issue).
type Input struct {
	Task  string // free-form task text
	Issue *Issue // a fetched issue
}

// Issue is the subset of a GitHub issue used in the prompt. The host fetches it
// (M4); M3 only renders it.
type Issue struct {
	Number int
	Title  string
	Body   string
	URL    string
}

// instructions is the fixed guidance appended to every prompt. It is
// intentionally identical across task and issue inputs so agent behaviour does
// not depend on how the task was supplied.
const instructions = `## Instructions

- Implement the change described above in the repository at your current working directory.
- Commit your work locally with clear, focused commit messages. Do NOT push.
- Do not add, change, or remove git remotes, and do not alter git configuration.
- If you cannot complete the task, commit what you have and explain why in the final commit message.
`

// Render produces the prompt markdown for in. It returns an error if the input
// is empty or ambiguous (neither or both of Task/Issue set).
func Render(in Input) (string, error) {
	switch {
	case in.Task == "" && in.Issue == nil:
		return "", fmt.Errorf("empty prompt input: set either Task or Issue")
	case in.Task != "" && in.Issue != nil:
		return "", fmt.Errorf("ambiguous prompt input: set only one of Task or Issue")
	}

	var b strings.Builder
	if in.Issue != nil {
		iss := in.Issue
		fmt.Fprintf(&b, "# Issue #%d: %s\n\n", iss.Number, strings.TrimSpace(iss.Title))
		if body := strings.TrimSpace(iss.Body); body != "" {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
		if iss.URL != "" {
			fmt.Fprintf(&b, "Source: %s\n\n", iss.URL)
		}
	} else {
		b.WriteString("# Task\n\n")
		b.WriteString(strings.TrimSpace(in.Task))
		b.WriteString("\n\n")
	}
	b.WriteString(instructions)
	return b.String(), nil
}
