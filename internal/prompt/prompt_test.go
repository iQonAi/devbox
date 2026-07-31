package prompt

import (
	"strings"
	"testing"
)

func TestRenderTask(t *testing.T) {
	got, err := Render(Input{Task: "  Add a health endpoint  "})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "# Task\n\nAdd a health endpoint\n\n" + instructions
	if got != want {
		t.Errorf("task prompt mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRenderIssue(t *testing.T) {
	got, err := Render(Input{Issue: &Issue{
		Number: 42, Title: "Login is broken", Body: "Steps to reproduce...", URL: "https://github.com/o/r/issues/42",
	}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"# Issue #42: Login is broken\n",
		"Steps to reproduce...",
		"Source: https://github.com/o/r/issues/42",
		instructions,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("issue prompt missing %q\nin:\n%s", want, got)
		}
	}
}

// Determinism: identical input renders byte-identical output.
func TestRenderDeterministic(t *testing.T) {
	in := Input{Issue: &Issue{Number: 1, Title: "t", Body: "b"}}
	a, _ := Render(in)
	b, _ := Render(in)
	if a != b {
		t.Error("render is not deterministic")
	}
}

func TestRenderRejectsBadInput(t *testing.T) {
	cases := map[string]Input{
		"empty":     {},
		"ambiguous": {Task: "x", Issue: &Issue{Number: 1}},
	}
	for name, in := range cases {
		if _, err := Render(in); err == nil {
			t.Errorf("%s: expected an error, got nil", name)
		}
	}
}
