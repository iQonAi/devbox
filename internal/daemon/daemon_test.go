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

	"github.com/iQonAi/devbox/internal/config"
)

func TestRunServesOverSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent-task.sock")
	cfg := &config.Config{
		SocketPath: sock,
		DataDir:    dir,
		Repos: []config.Repo{{
			Name: "devbox", Owner: "iQonAi", Repo: "devbox",
			DefaultBranch: "main", TokenRef: "gh-token-devbox",
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
	if len(repos) != 1 || repos[0].Name != "devbox" {
		t.Fatalf("got %+v, want the seeded devbox repo", err)
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
