package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/iQonAi/devbox/internal/store"
)

// newTestServer returns an api.Server backed by a fresh temp-dir database.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return NewServer(s, nil)
}

// get issues an in-memory request againstg the route -- no socket, no port.
func get(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	rec := get(t, newTestServer(t), "/v1/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReposEndpoint(t *testing.T) {
	srv := newTestServer(t)
	if err := srv.store.UpsertRepo(store.Repo{
		Name: "devbox", Owner: "iQonAi", Repo: "devbox",
		DefaultBranch: "main", TokenRef: "gh-token-devbox",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := get(t, srv, "/v1/repos")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var repos []store.Repo
	if err := json.Unmarshal(rec.Body.Bytes(), &repos); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body)
	}
	if len(repos) != 1 || repos[0].Name != "devbox" {
		t.Fatalf("got %+v, want one repo named devbox", repos)
	}
	if repos[0].TokenRef != "gh-token-devbox" {
		t.Errorf("token_ref = %q", repos[0].TokenRef)
	}
}

func TestTasksEmptyIsArray(t *testing.T) {
	rec := get(t, newTestServer(t), "/v1/tasks")
	if got := rec.Body.String(); got != "[]\n" {
		t.Errorf("body = %q, want %q", got, "[]\n")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/repos", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rec.Code)
	}
}
