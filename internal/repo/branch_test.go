package repo

import "testing"

func TestBranchName(t *testing.T) {
	cases := []struct {
		agent, title, id, want string
	}{
		{"claude", "Add login flow", "a1b2c3d", "agent/claude/add-login-flow-a1b2c3d"},
		{"claude", "Fix Bug #42!!!", "deadbee", "agent/claude/fix-bug-42-deadbee"},
		{"Codex", "   Spaces   ", "0000000", "agent/codex/spaces-0000000"},
		{"claude", "", "abc1234", "agent/claude/task-abc1234"},
		{"pi", "----", "abc1234", "agent/pi/task-abc1234"},
	}
	for _, c := range cases {
		if got := BranchName(c.agent, c.title, c.id); got != c.want {
			t.Errorf("BranchName(%q,%q,%q) = %q, want %q", c.agent, c.title, c.id, got, c.want)
		}
	}
}

func TestSlugifyCaps(t *testing.T) {
	long := "this is a realy quite excessively long issue title that keeps going"
	got := slugify(long)
	if len(got) > 40 {
		t.Errorf("slug not capped: %d chars (%q)", len(got), got)
	}
}
