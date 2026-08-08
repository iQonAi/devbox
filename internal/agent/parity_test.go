package agent

import (
	"strings"
	"testing"
)

// TestAdapterContractParity runs the adapter contract against every agent in
// the adapters registry (agent.go) — registering a new agent in Lookup fails
// this suite until a contract case is added to the table below. Contract:
// stable Name matching the Lookup key, an env var per supported auth method,
// errors for unsupported methods, and a Command that reads the prompt file
// and redirects a transcript to transcriptPath.
func TestAdapterContractParity(t *testing.T) {
	const (
		promptPath     = "/task/prompt.md"
		transcriptPath = "/task/out/transcript.json"
	)

	cases := []struct {
		name        string
		agent       Agent
		supported   []AuthMethod
		unsupported []AuthMethod
	}{
		{
			name:        "claude",
			agent:       Claude(),
			supported:   []AuthMethod{AuthSubscription, AuthAPIKey},
			unsupported: []AuthMethod{"bogus"},
		},
		{
			name:        "pi",
			agent:       Pi(),
			supported:   []AuthMethod{AuthAPIKey},
			unsupported: []AuthMethod{AuthSubscription, "bogus"},
		},
		{
			// The mock accepts every method — it makes no model calls.
			name:      "mock",
			agent:     Mock(),
			supported: []AuthMethod{AuthSubscription, AuthAPIKey},
		},
	}

	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		covered[tc.name] = true
	}
	for name := range adapters {
		if !covered[name] {
			t.Errorf("registered agent %q has no contract case in this suite", name)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.agent.Name() != tc.name {
				t.Errorf("Name() = %q, want %q", tc.agent.Name(), tc.name)
			}

			looked, err := Lookup(tc.name)
			if err != nil {
				t.Fatalf("Lookup(%q): %v", tc.name, err)
			}
			if looked.Name() != tc.name {
				t.Errorf("Lookup(%q).Name() = %q", tc.name, looked.Name())
			}

			for _, m := range tc.supported {
				envVar, err := tc.agent.EnvVar(m)
				if err != nil {
					t.Errorf("EnvVar(%q): %v", m, err)
				}
				if envVar == "" {
					t.Errorf("EnvVar(%q) is empty", m)
				}

				cmd, err := tc.agent.Command(m, promptPath, transcriptPath)
				if err != nil {
					t.Fatalf("Command(%q): %v", m, err)
				}
				for _, want := range []string{promptPath, transcriptPath, "> " + transcriptPath} {
					if !strings.Contains(cmd, want) {
						t.Errorf("Command(%q) missing %q:\n%s", m, want, cmd)
					}
				}
			}

			for _, m := range tc.unsupported {
				if _, err := tc.agent.EnvVar(m); err == nil {
					t.Errorf("EnvVar(%q): expected error", m)
				}
				if _, err := tc.agent.Command(m, promptPath, transcriptPath); err == nil {
					t.Errorf("Command(%q): expected error", m)
				}
			}
		})
	}

	if _, err := Lookup("no-such-agent"); err == nil {
		t.Error("Lookup of unknown agent: expected error")
	}
}

func TestPiCommandFlags(t *testing.T) {
	cmd, err := Pi().Command(AuthAPIKey, "/task/prompt.md", "/task/out/transcript.json")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	// --mode json is the machine-readable transcript; the prompt is fed via
	// stdin so content starting with '-' or '@' is never misparsed as an
	// option or @file; the jq guard turns an in-transcript agent failure into
	// a non-zero exit (pi's json mode alone exits 0 on model errors).
	for _, want := range []string{"pi ", "--mode json", "--provider anthropic", "< /task/prompt.md", "jq -es", "stopReason"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
}
