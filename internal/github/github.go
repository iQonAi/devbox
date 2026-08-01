// Package github performs host-side GitHub operations — issue fetch, feature-
// branch push, PR create, issue comment — using a repo-scoped machine-user
// token (D3). The token lives ONLY here and only on the host: it is passed to
// gh via the environment (never argv) and to git via a one-shot credential
// helper, and it is never placed in a container, the source export, the bundle,
// or the DB. A compromised agent has no token to steal.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/iQonAi/devbox/internal/gitx"
	"github.com/iQonAi/devbox/internal/prompt"
)

// tokenEnvVar carries the token to git's credential helper via the environment.
const tokenEnvVar = "AGENT_TASK_GH_TOKEN"

const credentialHelper = `!f() { echo username=x-access-token; echo password=$` + tokenEnvVar + `; }; f`

// Client runs GitHub operations for one repo with one token.
type Client struct {
	owner string
	repo  string
	token string
}

// New returns a client for owner/repo authenticating with token.
func New(owner, repo, token string) *Client {
	return &Client{owner: owner, repo: repo, token: token}
}

func (c *Client) slug() string { return c.owner + "/" + c.repo }

// gh runs the gh CLI with the token in GH_TOKEN (not argv) and returns stdout.
func (c *Client) gh(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Env = append(os.Environ(), "GH_TOKEN="+c.token, "GH_PROMPT_DISABLED=1")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// args carry no secret (the token is in the env), so they are safe to echo.
		return "", fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// FetchIssue renders an issue into the prompt input (D8 --issue).
func (c *Client) FetchIssue(ctx context.Context, number int) (prompt.Issue, error) {
	out, err := c.gh(ctx, "issue", "view", strconv.Itoa(number),
		"--repo", c.slug(), "--json", "number,title,body,url")
	if err != nil {
		return prompt.Issue{}, err
	}
	return parseIssue(out)
}

// parseIssue decodes the gh issue-view JSON. Split out so it is testable
// without invoking gh.
func parseIssue(jsonStr string) (prompt.Issue, error) {
	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return prompt.Issue{}, fmt.Errorf("parse issue json: %w", err)
	}
	return prompt.Issue{Number: raw.Number, Title: raw.Title, Body: raw.Body, URL: raw.URL}, nil
}

// Push pushes the feature branch to origin using the token via a credential
// helper (never in argv or written to .git/config). The worktree's origin is
// the repo's HTTPS URL from the mirror clone. All git runs hook-disabled (gitx).
func (c *Client) Push(ctx context.Context, worktreeDir, branch string) error {
	env := []string{tokenEnvVar + "=" + c.token}
	args := []string{
		"-c", "credential.helper=",
		"-c", "credential.helper=" + credentialHelper,
		"push", "origin", branch,
	}
	if _, err := gitx.RunEnv(ctx, worktreeDir, env, args...); err != nil {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	return nil
}

// OpenPR opens a pull request (base = the default branch) and returns its URL.
// Never auto-merged.
func (c *Client) OpenPR(ctx context.Context, branch, base, title, body string) (string, error) {
	bodyFile, err := os.CreateTemp("", "pr-body-")
	if err != nil {
		return "", fmt.Errorf("pr body file: %w", err)
	}
	defer os.Remove(bodyFile.Name())
	if _, err := bodyFile.WriteString(body); err != nil {
		bodyFile.Close()
		return "", fmt.Errorf("write pr body: %w", err)
	}
	bodyFile.Close()

	out, err := c.gh(ctx, "pr", "create", "--repo", c.slug(),
		"--base", base, "--head", branch, "--title", title, "--body-file", bodyFile.Name())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil // gh prints the PR URL
}

// CommentIssue posts a comment (used to back-link the PR onto the source issue).
func (c *Client) CommentIssue(ctx context.Context, number int, body string) error {
	_, err := c.gh(ctx, "issue", "comment", strconv.Itoa(number), "--repo", c.slug(), "--body", body)
	return err
}
