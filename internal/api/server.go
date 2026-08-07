package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/iQonAi/devbox/internal/store"
)

var (
	errNoSubmitter  = errors.New("task submission is not available on this server")
	errTaskNotFound = errors.New("task not found")
)

// SubmitRequest is the body of POST /v1/tasks.
type SubmitRequest struct {
	Repo  string `json:"repo"`
	Agent string `json:"agent"`
	Task  string `json:"task,omitempty"`
	Issue int    `json:"issue,omitempty"`
}

// Submitter enqueues and cancels tasks. Implemented by the daemon; nil for the
// read-only server (M0/M1 tests).
type Submitter interface {
	Submit(SubmitRequest) (taskID string, err error)
	Cancel(taskID string) error
}

// Server holds what the handlers need.
type Server struct {
	store *store.Store
	sub   Submitter
}

func NewServer(s *store.Store, sub Submitter) *Server {
	return &Server{store: s, sub: sub}
}

// Handler builds the route table. http.ServeMux is the stdlib router; modern Go
// lets the method be part of the pattern, so a POST /v1/repos 405s for free.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/repos", s.handleRepos)
	mux.HandleFunc("GET /v1/tasks", s.handleTasks)
	mux.HandleFunc("GET /v1/tasks/{id}", s.handleTaskStatus)
	mux.HandleFunc("POST /v1/tasks", s.handleSubmit)
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", s.handleCancel)
	return mux
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	if s.sub == nil {
		writeError(w, http.StatusServiceUnavailable, errNoSubmitter)
		return
	}
	var sr SubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&sr); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.sub.Submit(sr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"task_id": id})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if s.sub == nil {
		writeError(w, http.StatusServiceUnavailable, errNoSubmitter)
		return
	}
	if err := s.sub.Cancel(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelling"})
}

// handleTaskStatus returns a task plus its audit events.
func (s *Server) handleTaskStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tasks, err := s.store.ListTasks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	var found *store.Task
	for i := range tasks {
		if tasks[i].ID == id {
			found = &tasks[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound, errTaskNotFound)
		return
	}
	events, err := s.store.ListEvents(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if events == nil {
		events = []store.Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": found, "events": events})
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.store.ListRepos()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
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
		return
	}

	if tasks == nil {
		tasks = []store.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// writeJSON sets the header, status, and body - in that order, which matters:
// after WriteHeader the headers are already on the wire.
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
