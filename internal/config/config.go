package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultSocketPath = "/run/agent-task/agent-task.sock"

// Defaults applied when a field is left unset in config.yaml
const (
	defaultDataDir       = "/var/lib/agent-task"
	defaultMaxConcurrent = 2
	defaultTaskTimeout   = "30m"
	defaultBranch        = "main"
	defaultImage         = "localhost/devbox-agent-base:dev"
	defaultPodman        = "podman"
)

// Config is the whole config.yaml, parsed into go
type Config struct {
	SocketPath string                 `yaml:"socket_path"`
	DataDir    string                 `yaml:"data_dir"`
	WorkDir    string                 `yaml:"work_dir"` // shared scratch root (agentbox-accessible); "" = data_dir/work
	Image      string                 `yaml:"image"`
	Podman     string                 `yaml:"podman"`
	Limits     Limits                 `yaml:"limits"`
	Repos      []Repo                 `yaml:"repos"`
	Agents     map[string]AgentConfig `yaml:"agents"`
}

// AgentConfig is the per-agent auth policy. token_ref names a LoadCredential
// secret (the model key); the secret itself never appears in config (D3).
type AgentConfig struct {
	Auth     string `yaml:"auth"`      // "subscription" | "api_key"
	TokenRef string `yaml:"token_ref"` // LoadCredential name for the model credential
}

// Limits are the daemon's resource caps (D10)
// TaskTimeout is a string like "30m' here; we parse it in the validation step,
// because yaml.v3 can't turn "30m" into a time.Duration on it's own
type Limits struct {
	MaxConcurrent int    `yaml:"max_concurrent"`
	TaskTimeout   string `yaml:"task_timeout"`
}

// Repo is one entry in the static registry (D11): a short name mapped to a
// Github repo plus the LoadCredentials secret NAME (never the token itself).
type Repo struct {
	Name          string `yaml:"name"`
	Owner         string `yaml:"owner"`
	Repo          string `yaml:"repo"`
	DefaultBranch string `yaml:"default_branch"`
	TokenRef      string `yaml:"token_ref"`
}

// Load reads and parses the YAML config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}

	return &cfg, nil
}

// applyDefaults fills unset fields with sensible defaults
func (c *Config) applyDefaults() {
	if c.SocketPath == "" {
		c.SocketPath = DefaultSocketPath
	}

	if c.DataDir == "" {
		c.DataDir = defaultDataDir
	}

	if c.Limits.MaxConcurrent == 0 {
		c.Limits.MaxConcurrent = defaultMaxConcurrent
	}

	if c.Limits.TaskTimeout == "" {
		c.Limits.TaskTimeout = defaultTaskTimeout
	}

	if c.Image == "" {
		c.Image = defaultImage
	}

	if c.Podman == "" {
		c.Podman = defaultPodman
	}

	// range give a COPY of each element, so index by i to mutate the slice
	for i := range c.Repos {
		if c.Repos[i].DefaultBranch == "" {
			c.Repos[i].DefaultBranch = defaultBranch
		}
	}
}

// Validate checks the config is complete and internally consistent
func (c *Config) Validate() error {
	if len(c.Repos) == 0 {
		return fmt.Errorf("no repos configured: the registry needs at least one entry")
	}

	if _, err := time.ParseDuration(c.Limits.TaskTimeout); err != nil {
		return fmt.Errorf("limits.task_timeout %q is not a valid duration: %w", c.Limits.TaskTimeout, err)
	}

	if c.Limits.MaxConcurrent < 1 {
		return fmt.Errorf("limits.max_concurrent must be >= 1, got %d", c.Limits.MaxConcurrent)
	}

	seen := make(map[string]bool)
	for i, r := range c.Repos {
		switch {
		case r.Name == "":
			return fmt.Errorf("repos[%d]: name is required", i)
		case seen[r.Name]:
			return fmt.Errorf("repos[%d]: duplicate repo name %q", i, r.Name)
		case r.Owner == "" || r.Repo == "":
			return fmt.Errorf("repo %q: owner and repo are required", r.Name)
		case r.TokenRef == "":
			return fmt.Errorf("repo %q: token_ref is required (the LoadCredential secret name)", r.Name)
		}
		seen[r.Name] = true
	}

	for name, a := range c.Agents {
		switch a.Auth {
		case "subscription", "api_key":
		default:
			return fmt.Errorf("agent %q: auth must be subscription or api_key, got %q", name, a.Auth)
		}
		if a.TokenRef == "" {
			return fmt.Errorf("agent %q: token_ref is required", name)
		}
	}
	return nil
}
