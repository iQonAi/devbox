package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The example config we ship must always be valid - this gaurds against
// comitting a broken config.example.yaml
func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("example config should load, got %v", err)
	}
	if len(cfg.Repos) == 0 {
		t.Fatalf("example config should have at least on repo")
	}
}

// Validation should reject broken configs. Table-driven: each entry is small
// YAML doc we expect Load to reject.
func TestLoadRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"no repos":          "repos: []\n",
		"duplicate name":    "repos:\n  - {name: a, owner: o, repo: r, token_ref: t}\n  - {name: a, owner: o, repo: r2, token_ref: t2}\n",
		"missing token_ref": "repos:\n  - {name: a, owner: o, repo: r}\n",
		"bad timeout":       "limits: {task_timeout: banana}\nrepos:\n  - {name: a, owner: o, repo: r, token_ref: t}\n",
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeTempConfig(t, doc)
			if _, err := Load(path); err == nil {
				t.Fatalf("expected an error for %q, got nil", name)
			}
		})
	}
}

// A minimal valid config should load and get defaults applied
func TestLoadAppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, "repos:\n - {name: a, owner: o, repo: r, token_ref: t}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("valid config failed to load: %v", err)
	}
	if cfg.Limits.MaxConcurrent != 2 {
		t.Errorf("max_concurrent default = %d, want 2", cfg.Limits.MaxConcurrent)
	}
	if cfg.Repos[0].DefaultBranch != "main" {
		t.Errorf("default_branch default = %q, want main", cfg.Repos[0].DefaultBranch)
	}
}

// writeTempConfig writes content to a temp file and returns its path
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadAgents(t *testing.T) {
	path := writeTempConfig(t, "repos:\n  - {name: a, owner: o, repo: r, token_ref: t}\n"+
		"agents:\n  claude: {auth: subscription, token_ref: claude-oauth-token}\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents["claude"].Auth != "subscription" || cfg.Agents["claude"].TokenRef != "claude-oauth-token" {
		t.Errorf("agent config not parsed: %+v", cfg.Agents["claude"])
	}
	if cfg.Image == "" || cfg.Podman == "" {
		t.Errorf("image/podman defaults not applied: image=%q podman=%q", cfg.Image, cfg.Podman)
	}
}

func TestLoadRejectsBadAgentAuth(t *testing.T) {
	path := writeTempConfig(t, "repos:\n  - {name: a, owner: o, repo: r, token_ref: t}\n"+
		"agents:\n  claude: {auth: bogus, token_ref: x}\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for bad agent auth")
	}
}
