// Package agent adapts coding agents to the runner behind one interface (D7,
// §8.8). Each adapter maps an auth method to the env var the agent reads and
// produces the shell command that runs it non-interactively against the source.
// Claude lands here in M3; Pi proves the abstraction in M6 (D7 amendment:
// pi promoted over codex); Codex is deferred.
package agent

import (
	"fmt"
	"path"
)

// AuthMethod is how an agent authenticates to its model. Subscription uses an
// OAuth token (Claude/Codex); api_key uses a provider API key (Pi, or as an
// option for the others).
type AuthMethod string

const (
	AuthSubscription AuthMethod = "subscription"
	AuthAPIKey       AuthMethod = "api_key"
)

// Agent is one coding-agent adapter.
type Agent interface {
	// Name is the adapter's stable identifier (matches the --agent flag).
	Name() string
	// EnvVar returns the environment variable the agent reads its model
	// credential from, under the given auth method.
	EnvVar(method AuthMethod) (string, error)
	// Command returns a shell snippet — run via `bash -c`, with the source repo
	// as the working directory — that runs the agent non-interactively against
	// the prompt at promptPath, writes its transcript to transcriptPath, and
	// best-effort extracts the PR summary into summary.txt next to the
	// transcript (each adapter knows its own transcript shape; the controller's
	// wrapper stays agent-agnostic). It must exit non-zero when the agent
	// fails, and the summary step must never change that exit status.
	Command(method AuthMethod, promptPath, transcriptPath string) (string, error)
}

// summaryPath is where a Command snippet writes the PR summary: summary.txt as
// a sibling of the transcript, derived from the transcriptPath argument so it
// follows the out dir wherever the controller puts it. path (not filepath):
// these are in-container POSIX paths regardless of host OS.
func summaryPath(transcriptPath string) string {
	return path.Dir(transcriptPath) + "/summary.txt"
}

// adapters maps each registered agent name to its constructor. Lookup and the
// adapter-contract parity suite both consume it, so registering an agent here
// fails the suite until a matching contract case is added.
var adapters = map[string]func() Agent{
	"claude": Claude,
	"pi":     Pi,
	"mock":   Mock,
}

// Lookup returns the adapter for name, or an error if unknown.
func Lookup(name string) (Agent, error) {
	if newAgent, ok := adapters[name]; ok {
		return newAgent(), nil
	}
	return nil, fmt.Errorf("unknown agent %q", name)
}
