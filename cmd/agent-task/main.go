package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/iQonAi/devbox/internal/agent"
	"github.com/iQonAi/devbox/internal/client"
	"github.com/iQonAi/devbox/internal/config"
	"github.com/iQonAi/devbox/internal/controller"
	"github.com/iQonAi/devbox/internal/daemon"
	"github.com/iQonAi/devbox/internal/prompt"
	"github.com/iQonAi/devbox/internal/repo"
	"github.com/iQonAi/devbox/internal/runner"
)

const defaultConfigPath = "/etc/agent-task/config.yaml"

func main() {
	// os.Args[0] is the porgram name; os.args[1] is the subcommand (if any).
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "repos":
		err = runRepos(os.Args[2:])
	case "ls":
		err = runLs(os.Args[2:])
	case "run":
		err = runRun(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-task: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agent-task - devbox orchestrator

usage:
	agent-task serve [--config PATH]	start the daemon (Unix socket)
	agent-task repos [--socket PATH]	list registered repositories
	agent-task ls [--socket PATH]	list tasks
	agent-task run --task TEXT --repo-url URL [--agent claude]	run an agent task (M3)
`)
}

// runRun executes a single agent task end-to-end (M3). Standalone, in-process;
// the daemon/worker-pool path is M5.
func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	repoName := fs.String("repo", "", "registered repo name (from config); or use --repo-url")
	repoURL := fs.String("repo-url", "", "source repo URL (file:// or https://); overrides the registry")
	defaultBranch := fs.String("default-branch", "", "default branch (default: registry value or 'main')")
	taskText := fs.String("task", "", "free-form task text (required; --issue lands in M4)")
	agentName := fs.String("agent", "claude", "agent adapter: claude|mock")
	authStr := fs.String("auth", "subscription", "auth method: subscription|api_key")
	tokenFile := fs.String("model-token-file", "", "file holding the model token; else inherit the agent's env var")
	image := fs.String("image", "localhost/devbox-agent-base:dev", "agent base image")
	podman := fs.String("podman", "podman", "podman command, e.g. 'sudo -u agentbox /usr/local/sbin/agentbox-podman'")
	dataDir := fs.String("data-dir", "", "mirror cache dir (default: config data_dir)")
	workDir := fs.String("work-dir", "", "scratch dir for this run (default: a fresh temp dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *taskText == "" {
		return fmt.Errorf("--task is required")
	}
	if *repoURL == "" && *repoName == "" {
		return fmt.Errorf("one of --repo or --repo-url is required")
	}

	rName, rURL, rBranch, rTokenRef := *repoName, *repoURL, *defaultBranch, ""
	if *repoName != "" && *repoURL == "" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			return err
		}
		var found *config.Repo
		for i := range cfg.Repos {
			if cfg.Repos[i].Name == *repoName {
				found = &cfg.Repos[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("repo %q is not in the registry", *repoName)
		}
		rURL = fmt.Sprintf("https://github.com/%s/%s.git", found.Owner, found.Repo)
		rTokenRef = found.TokenRef
		if rBranch == "" {
			rBranch = found.DefaultBranch
		}
	}
	if rName == "" {
		rName = "task-repo"
	}
	if rBranch == "" {
		rBranch = "main"
	}

	ag, err := agent.Lookup(*agentName)
	if err != nil {
		return err
	}

	var authValue string
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fmt.Errorf("read model token: %w", err)
		}
		authValue = strings.TrimSpace(string(b))
	}

	wd := *workDir
	if wd == "" {
		if wd, err = os.MkdirTemp("", "agent-run-"); err != nil {
			return err
		}
	}
	dd := *dataDir
	if dd == "" {
		if cfg, err := config.Load(*configPath); err == nil {
			dd = cfg.DataDir
		} else {
			dd = wd
		}
	}

	deps := controller.Deps{
		Repo:   repo.NewManager(dd),
		Runner: runner.NewPodmanRunner(strings.Fields(*podman), nil),
		Image:  *image,
	}
	req := controller.Request{
		TaskID:        fmt.Sprintf("t%d", time.Now().UnixNano()),
		Title:         *taskText,
		RepoName:      rName,
		RepoURL:       rURL,
		DefaultBranch: rBranch,
		TokenRef:      rTokenRef,
		Prompt:        prompt.Input{Task: *taskText},
		Agent:         ag,
		AuthMethod:    agent.AuthMethod(*authStr),
		AuthValue:     authValue,
		WorkDir:       wd,
		Limits:        controller.Limits{CPUs: "2", MemoryMB: 2048, PidsLimit: 256, Timeout: 30 * time.Minute},
	}

	out, err := controller.Run(context.Background(), deps, req)
	if err != nil {
		return err
	}

	fmt.Printf("state:    %s\n", out.State)
	fmt.Printf("commits:  %d\n", out.Commits)
	fmt.Printf("exit:     %d\n", out.ExitCode)
	fmt.Printf("branch:   %s\n", out.Branch)
	fmt.Printf("worktree: %s\n", out.Worktree)
	for _, a := range out.Artifacts {
		fmt.Printf("artifact: %-10s %s\n", a.Kind, a.Path)
	}
	if out.State != controller.StateCompleted {
		return fmt.Errorf("task did not complete (state=%s, exit=%d)", out.State, out.ExitCode)
	}
	return nil
}

// runServe parses the serve flags and runs the daemon until SIGINT/SIGTERM.
func runServe(args []string) error {
	// the global flag package can't express subcommands, so each one get its
	// own FlagSet parsing the args after teh subcommand name
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "path to config.yaml")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// structured logs to stderr; systemd captures them into the journal
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	// Cancels ctx on the first SIGINT/SIGTERM. A second signal falls through to
	// the default handler, so a wedged daemon can still be killed.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return daemon.Run(ctx, cfg)
}

func runRepos(args []string) error {
	fs := flag.NewFlagSet("repos", flag.ExitOnError)
	socket := fs.String("socket", config.DefaultSocketPath, "daemon socket path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repos, err := client.New(*socket).Repos()
	if err != nil {
		return err
	}

	// tabwriter bufferes every row, then pads columns to the widest cell - which
	// is why nothing prints until Flush
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tOWNER\tREPO\tBRANCH\tTOKEN_REF")
	for _, r := range repos {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Name, r.Owner, r.Repo, r.DefaultBranch, r.TokenRef)
	}
	return tw.Flush()
}

func runLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	socket := fs.String("socket", config.DefaultSocketPath, "daemon socket path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	tasks, err := client.New(*socket).Tasks()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO_ID\tSOURCE\tSTATE\tCREATED")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", t.ID, t.RepoID, t.Source, t.State, t.CreatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}
