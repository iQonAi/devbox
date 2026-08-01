package agent

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, name := range []string{"claude", "mock"} {
		a, err := Lookup(name)
		if err != nil {
			t.Fatalf("lookup %q: %v", name, err)
		}
		if a.Name() != name {
			t.Errorf("Lookup(%q).Name() = %q", name, a.Name())
		}
	}
	if _, err := Lookup("nope"); err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestClaudeEnvVar(t *testing.T) {
	c := Claude()
	sub, _ := c.EnvVar(AuthSubscription)
	if sub != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("subscription env = %q", sub)
	}
	key, _ := c.EnvVar(AuthAPIKey)
	if key != "ANTHROPIC_API_KEY" {
		t.Errorf("api_key env = %q", key)
	}
	if _, err := c.EnvVar("bogus"); err == nil {
		t.Error("expected error for unknown auth method")
	}
}

func TestClaudeCommand(t *testing.T) {
	cmd, err := Claude().Command(AuthSubscription, "/task/prompt.md", "/task/out/transcript.json")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	for _, want := range []string{"claude -p", "/task/prompt.md", "--output-format json", "--dangerously-skip-permissions", "/task/out/transcript.json"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}
	// --bare would ignore the OAuth token subscription auth needs.
	if strings.Contains(cmd, "--bare") {
		t.Error("command uses --bare, incompatible with CLAUDE_CODE_OAUTH_TOKEN")
	}
}

func TestClaudeCommandRejectsBadAuth(t *testing.T) {
	if _, err := Claude().Command("bogus", "/p", "/t"); err == nil {
		t.Error("expected error for unsupported auth method")
	}
}

func TestMockCommandCommits(t *testing.T) {
	cmd, err := Mock().Command(AuthAPIKey, "/task/prompt.md", "/task/out/transcript.json")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	for _, want := range []string{"/task/prompt.md", "mock_change.txt", "git commit"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("mock command missing %q:\n%s", want, cmd)
		}
	}
}
