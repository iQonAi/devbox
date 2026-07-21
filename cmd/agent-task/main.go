package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/iQonAi/devbox/internal/client"
	"github.com/iQonAi/devbox/internal/config"
	"github.com/iQonAi/devbox/internal/daemon"
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
	default:
		fmt.Fprintf(os.Stderr, "unknonw command %q\n\n", os.Args[1])
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
`)
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
	fmt.Fprintln(tw, "NAME\tOwner\tREPO\tBRANCH\tTOKEN_REF")
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
	fmt.Fprintln(tw, "ID\tREPO_ID\tSOURCE\tSTATE\tCreated")
	for _, t := range tasks {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", t.ID, t.RepoID, t.Source, t.State, t.CreatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}
