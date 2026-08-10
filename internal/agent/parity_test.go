package agent

import (
	"strings"
	"testing"
)

// TestAdapterContractParity runs the adapter contract against every agent in
// the adapters registry (agent.go) — registering a new agent in Lookup fails
// this suite until a contract case is added to the table below. Contract:
// stable Name matching the Lookup key, an env var per supported auth method,
// errors for unsupported methods, and a Command that reads the prompt file,
// redirects a transcript to transcriptPath, and redirects a summary to
// summary.txt next to the transcript (#36: summary extraction is
// adapter-owned; the controller's wrapper has no agent-specific parsing).
func TestAdapterContractParity(t *testing.T) {
	const (
		promptPath     = "/task/prompt.md"
		transcriptPath = "/task/out/transcript.json"
		summaryTarget  = "/task/out/summary.txt"
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
				if !strings.Contains(cmd, promptPath) {
					t.Errorf("Command(%q) missing prompt path %q:\n%s", m, promptPath, cmd)
				}
				// The contract says "redirects a transcript to transcriptPath"
				// without fixing the quoting, so require the path plus a `>`
				// redirect somewhere before its first occurrence.
				if idx := strings.Index(cmd, transcriptPath); idx < 0 {
					t.Errorf("Command(%q) missing transcript path %q:\n%s", m, transcriptPath, cmd)
				} else if !strings.Contains(cmd[:idx], ">") {
					t.Errorf("Command(%q) has no `>` redirect before %q:\n%s", m, transcriptPath, cmd)
				}
				// The summary obligation: a `>` redirect targeting summary.txt
				// as a sibling of the transcript.
				if !strings.Contains(cmd, "> "+summaryTarget) {
					t.Errorf("Command(%q) does not redirect a summary to %q:\n%s", m, summaryTarget, cmd)
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
	// a non-zero exit (pi's json mode alone exits 0 on model errors); the
	// summary comes from the last assistant message_end's content (string or
	// text blocks), and the agent status is re-raised after extraction.
	for _, want := range []string{
		"pi ", "--mode json", "--provider anthropic", "< /task/prompt.md",
		"jq -es", "stopReason",
		"jq -rs", "message_end", "> /task/out/summary.txt", `(exit "$AGENT_STATUS")`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
}
