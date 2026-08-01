package github

import (
	"strings"
	"testing"
)

func TestParseIssue(t *testing.T) {
	js := `{"number":42,"title":"Login is broken","body":"Steps...","url":"https://github.com/o/r/issues/42"}`
	got, err := parseIssue(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Number != 42 || got.Title != "Login is broken" || got.URL == "" {
		t.Errorf("got %+v", got)
	}
}

func TestParseIssueBadJSON(t *testing.T) {
	if _, err := parseIssue("not json"); err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestBuildPRBody(t *testing.T) {
	body := BuildPRBody(PRInfo{
		TaskID: "t123", Agent: "claude",
		IssueURL: "https://github.com/o/r/issues/7",
		Summary:  "Added subtract().",
	})
	for _, want := range []string{
		"`t123`", "`claude`",
		"https://github.com/o/r/issues/7",
		"Added subtract().",
		"human review required",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q:\n%s", want, body)
		}
	}
	// The reworded marker must NOT carry the emoji AI-attribution pattern.
	if strings.Contains(body, "🤖") {
		t.Error("PR body contains the 🤖 AI-attribution emoji")
	}
}

func TestBuildPRBodyNoIssueNoSummary(t *testing.T) {
	body := BuildPRBody(PRInfo{TaskID: "t1", Agent: "mock"})
	if strings.Contains(body, "- Issue:") {
		t.Error("issue line present with no issue URL")
	}
	if !strings.Contains(body, "no summary") {
		t.Error("missing empty-summary placeholder")
	}
}

func TestBuildPRBodyTestOutput(t *testing.T) {
	body := BuildPRBody(PRInfo{TaskID: "t1", Agent: "claude", TestOutput: "ok 5 tests"})
	if !strings.Contains(body, "## Test results") || !strings.Contains(body, "ok 5 tests") {
		t.Errorf("test output not rendered:\n%s", body)
	}
}
