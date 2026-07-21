package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/iQonAi/devbox/internal/store"
)

// Server holds what the handlers need. M0 is read-only; task creation lands in M1
type Server struct {
	store *store.Store
}

func NewServer(s *store.Store) *Server {
	return &Server{store: s}
}

// Handler builds the route table. http.ServerMux is the stdlib router; modern Go
// lets the method be part of the pattern, so a POST /v1/repos 405s for free.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.handleHearthz)
	mux.HandleFunc("GET /v1/repos", s.handleRepos)
	mux.HandleFunc("GET /v1/tasks", s.handleTasks)
	return mux
}

func (s *Server) handleHearthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.store.ListRepos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}

	if repos == nil {
		repos = []store.Repo{}
	}
	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
	}

	if tasks == nil {
		tasks = []store.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// writeJSON sets the header, status, and body - in that order, which matters:
// after WriteHeader the headers are already on the write.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, code int, err error) {
	slog.Error("request failed", "error", err)
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
