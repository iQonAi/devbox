// Package agent adapts coding agents to the runner behind one interface (D7,
// §8.8). Each adapter maps an auth method to the env var the agent reads and
// produces the shell command that runs it non-interactively against the source.
// Claude lands here in M3; Codex (M6) and Pi prove the abstraction later.
package agent

import "fmt"

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
	// the prompt at promptPath and writes its transcript to transcriptPath. It
	// must exit non-zero when the agent fails.
	Command(method AuthMethod, promptPath, transcriptPath string) (string, error)
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
