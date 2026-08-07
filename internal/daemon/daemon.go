package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iQonAi/devbox/internal/api"
	"github.com/iQonAi/devbox/internal/config"
	"github.com/iQonAi/devbox/internal/controller"
	"github.com/iQonAi/devbox/internal/pool"
	"github.com/iQonAi/devbox/internal/repo"
	"github.com/iQonAi/devbox/internal/runner"
	"github.com/iQonAi/devbox/internal/store"
)

const (
	// 0660: owner is the service user, gorup is agent-taskd, which the operator
	// joins so the cli can connect. Nobody else can reach the API.
	socketMode = 0o660

	shutdownTimeout = 10 * time.Second
)

// Run opens the store, seeds the repo registry, and servers until ctx is cancelled
func Run(ctx context.Context, cfg *config.Config) error {
	dbPath := filepath.Join(cfg.DataDir, "agent-task.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := seedRepos(st, cfg); err != nil {
		return err
	}

	// Recover tasks left in-flight by a crash/restart: their containers and
	// contexts are gone, so they cannot resume — fail them so the sweep can
	// reclaim their worktrees and status is honest.
	if n := recoverInflight(st); n > 0 {
		slog.Warn("recovered interrupted tasks", "count", n)
	}

	// Sweep orphaned worktrees left by terminal tasks from a previous run.
	// Best-effort: a sweep failure must not stop the daemon from serving.
	mgr := repo.NewManager(cfg.DataDir)
	if n, err := mgr.Sweep(ctx, st); err != nil {
		slog.Error("orphan sweep failed", "error", err)
	} else if n > 0 {
		slog.Info("orphan sweep", "removed", n)
	}

	// Pipeline deps + worker pool (D10 concurrency cap). The pool drives
	// controller.Run for each submitted task; the store is the Recorder.
	deps := controller.Deps{
		Repo:     mgr,
		Runner:   runner.NewPodmanRunner(strings.Fields(cfg.Podman), nil),
		Image:    cfg.Image,
		Recorder: st,
	}
	runFn := func(runCtx context.Context, req controller.Request) (controller.Outcome, error) {
		out, err := controller.Run(runCtx, deps, req)
		if err != nil {
			// A pipeline error (not a terminal Outcome) must not leave the task
			// stuck in Running: record it as Failed and log the reason.
			slog.Error("task pipeline error", "task", req.TaskID, "error", err)
			_ = st.UpdateTaskState(req.TaskID, store.StateFailed)
			_ = st.InsertEvent(req.TaskID, store.EventState, "->Failed: "+err.Error())
		}
		return out, err
	}
	p := pool.New(ctx, runFn, cfg.Limits.MaxConcurrent, 128)
	sub := &submitter{cfg: cfg, store: st, pool: p}

	ln, err := listen(cfg.SocketPath)
	if err != nil {
		return err
	}

	srv := &http.Server{Handler: api.NewServer(st, sub).Handler()}

	// Serve blocks forever, so run it alongside and let either it or ctx end us.
	// Buffered, so the goroutine can exit even if nobody reads the error.
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	slog.Info("listening", "socket", cfg.SocketPath, "db", dbPath, "repos", len(cfg.Repos))
	if err := sdNotify("READY=1"); err != nil {
		slog.Warn("sd_notify READY failed", "error", err)
	}

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	if err := sdNotify("STOPPING=1"); err != nil {
		slog.Warn("sd_notify STOPPING failed", "error", err)
	}

	// Bounded grace period for in-flight requests, then drop them.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Workers were cancelled with ctx; wait for them to drain.
	p.Wait()
	return nil
}

// recoverInflight fails any task left in a non-terminal state by a previous
// daemon (crash or restart). Returns how many were recovered.
func recoverInflight(st *store.Store) int {
	tasks, err := st.ListTasks()
	if err != nil {
		slog.Error("recover: list tasks", "error", err)
		return 0
	}
	n := 0
	for _, t := range tasks {
		if t.State == store.StateCreated || t.State == store.StateRunning {
			_ = st.UpdateTaskState(t.ID, store.StateFailed)
			_ = st.InsertEvent(t.ID, store.EventState, "->Failed: interrupted by daemon restart")
			n++
		}
	}
	return n
}

// seedRepos mirrors the static config registry (D11) into the store, so the
// config file stays the source of truth across restarts.
func seedRepos(st *store.Store, cfg *config.Config) error {
	for _, r := range cfg.Repos {
		if err := st.UpsertRepo(store.Repo{
			Name:          r.Name,
			Owner:         r.Owner,
			Repo:          r.Repo,
			DefaultBranch: r.DefaultBranch,
			TokenRef:      r.TokenRef,
		}); err != nil {
			return fmt.Errorf("seed repo %q: %w", r.Name, err)
		}
	}
	return nil
}

// listen creates the unix socket, clearing one left behind by a crash
func listen(path string) (net.Listener, error) {
	// Under systemd the parent is RuntimeDirectory=; create it for dev/test runs.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	if err := removeStaleSocket(path); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}

	// The socket is created with the process umask applied, so set the mode we
	// actually want explicitly.
	if err := os.Chmod(path, socketMode); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return ln, nil
}

// removeStaleSocket deletes a leftover socket file, but only after proving no
// daemon is listening on it: a successful dial means we would be stealing a
// live socket, which must be a hard error instead
func removeStaleSocket(path string) error {
	// check if the file exists, if file not exists serr return nil or return stat error
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat socket: %w", err)
	}

	// A successful dial means a daemon is listening: refuse rather than steal it.
	// A failed dial means nothing is on the other end, so the file is stale.
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		conn.Close()
		return fmt.Errorf("socket %s is already in use by a running daemon", path)
	}

	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing stale socket: %w", err)
	}

	slog.Warn("removed stale socket", "path", path)
	return nil
}
