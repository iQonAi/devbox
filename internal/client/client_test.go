package client

import (
	"net"
	"net/http"
	"path/filepath"
	"testing"
)

// fakeDaemon serves handler on a temp Unix socket and returns its path.
func fakeDaemon(t *testing.T, handler http.Handler) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "fake.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return sock
}

func TestReposDecodes(t *testing.T) {
	sock := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/repos" {
			t.Errorf("path = %q, want /v1/repos", r.URL.Path)
		}
		w.Write([]byte(`[{"name":"agent-task","owner":"iQonAi","repo":"agent-task","default_branch":"main"}]`))
	}))

	repos, err := New(sock).Repos()
	if err != nil {
		t.Fatalf("repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Owner != "iQonAi" {
		t.Fatalf("got %+v", repos)
	}
}

func TestErrorStatusIsReported(t *testing.T) {
	sock := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	if _, err := New(sock).Tasks(); err == nil {
		t.Fatalf("expected an error for a 500 response, got nil")
	}
}

func TestNoDaemonIsAnError(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "nope.sock")).Repos(); err == nil {
		t.Fatalf("expected an error when the sock does not exist, got nil")
	}
}
