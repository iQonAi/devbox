package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iQonAi/agent-task/internal/config"
)

func TestRunServesOverSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent-task.sock")
	cfg := &config.Config{
		SocketPath: sock,
		DataDir:    dir,
		Repos: []config.Repo{{
			Name: "agent-task", Owner: "iQonAi", Repo: "agent-task",
			DefaultBranch: "main", TokenRef: "gh-token-agent-task",
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	waitForSocket(t, sock)

	// An HTTP client whose transport dials the Unix ssocket isntead of TCP. The
	// host in the URL is ignored, but a URL needs one to be valid.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}

	resp, err := client.Get("http://unix/v1/repos")
	if err != nil {
		t.Fatalf("get repos: %v", err)
	}
	defer resp.Body.Close()

	var repos []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(repos) != 1 || repos[0].Name != "agent-task" {
		t.Fatalf("got %+v, want the seeded agent-task repo", repos)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Run did not return after cancel")
	}

	// Closing the listener unlinks the socket file
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket sill present after shutdown: %v", err)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	for range 50 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket never appeared")
}

// A socket file left behind by a crash has nothing listening on it, so it is
// safe to remove. This path is what makes a restart-after-crash work.
func TestRemoveStaleSocketClearsDeadSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "dead.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Go unlinks a Unix socket on Close; disable that to simulate a crash,
	// where the process dies without ever running its cleanup.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	if err := removeStaleSocket(sock); err != nil {
		t.Fatalf("removeStaleSocket: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("stale socket not removed: %v", err)
	}
}

// A socket with a live daemon behind it must never be removed: that would
// silently cut every connected client loose.
func TestRemoveStaleSocketRefusesLiveSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "live.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if err := removeStaleSocket(sock); err == nil {
		t.Fatal("expected an error for a socket in use, got nil")
	}
	if _, err := os.Stat(sock); err != nil {
		t.Errorf("live socket was removed: %v", err)
	}
}
