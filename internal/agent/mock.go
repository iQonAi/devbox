package agent

import "fmt"

// mock is a deterministic agent used to prove the pipeline end-to-end without a
// live model: it records the prompt, makes one code change, and commits it —
// the same contract as a real agent (the agent commits; the wrapper bundles).
// The controller's wrapper sets a git identity before running it.
type mock struct{}

// Mock returns the deterministic test agent.
func Mock() Agent { return mock{} }

func (mock) Name() string { return "mock" }

// EnvVar returns a harmless placeholder — the mock makes no model calls.
func (mock) EnvVar(AuthMethod) (string, error) { return "MOCK_TOKEN", nil }

func (mock) Command(_ AuthMethod, promptPath, transcriptPath string) (string, error) {
	return fmt.Sprintf(
		`cat %s > %s; printf 'change by mock agent\n' > mock_change.txt; `+
			`git add mock_change.txt; git commit -qm 'mock: apply change'`,
		promptPath, transcriptPath,
	), nil
}
