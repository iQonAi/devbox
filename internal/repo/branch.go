package repo

import (
	"fmt"
	"regexp"
	"strings"
)

// unsafe matches an y run of characters not allowed in our slug: git ref rules
// forbid many bytes, and we are stricter still - only lowercase alphanumerics
// and single dashses survive.
var unsafe = regexp.MustCompile(`[^a-z0-9]+`)

// BranchName builds the feature-branch name for a task:
//
//	agent/<agent>/<slug>-<shortid>
//
// The agent/ prefix preserves the namespace for future Github branch-protection
// rules (design 8.3). slug is a sanitized form of the human title; shortid
// keeps the branch unique even when two tasks share a title.
func BranchName(agent, title, shortid string) string {
	return fmt.Sprintf("agent/%s/%s-%s", slugify(agent), slugify(title), shortid)
}

// slugify lowercases, replaces every run of disallowed characters with a single
// dash, and trims leading/trailing dashses. An empty result becomees "task" so
// the branch name is never malformed
func slugify(s string) string {
	s = unsafe.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "task"
	}

	// Cap the slug so a long  issue title cannot blow past filesystem name limits
	// once the full path is assembled.
	const maxSlug = 40
	if len(s) > maxSlug {
		s = strings.Trim(s[:maxSlug], "-")
	}
	return s
}
