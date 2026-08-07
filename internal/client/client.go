package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/iQonAi/devbox/internal/store"
)

const requestTimeout = 10 * time.Second

// thin http client bound to one socket
type Client struct {
	http   *http.Client
	socket string
}

func New(socketPath string) *Client {
	return &Client{
		socket: socketPath,
		http: &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				// ignore what ever the host URL names and always dial the socket
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *Client) Repos() ([]store.Repo, error) {
	var repos []store.Repo
	err := c.get("/v1/repos", &repos)
	return repos, err
}

func (c *Client) Tasks() ([]store.Task, error) {
	var tasks []store.Task
	err := c.get("/v1/tasks", &tasks)
	return tasks, err
}

// Submit enqueues a task on the daemon and returns the new task id.
func (c *Client) Submit(repo, agent, task string, issue int) (string, error) {
	body := map[string]any{"repo": repo, "agent": agent}
	if task != "" {
		body["task"] = task
	}
	if issue > 0 {
		body["issue"] = issue
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := c.post("/v1/tasks", body, &resp); err != nil {
		return "", err
	}
	return resp.TaskID, nil
}

// Cancel asks the daemon to cancel a running task.
func (c *Client) Cancel(taskID string) error {
	return c.post("/v1/tasks/"+taskID+"/cancel", nil, nil)
}

// Status returns a task and its audit events.
func (c *Client) Status(taskID string) (task store.Task, events []store.Event, err error) {
	var out struct {
		Task   store.Task    `json:"task"`
		Events []store.Event `json:"events"`
	}
	err = c.get("/v1/tasks/"+taskID, &out)
	return out.Task, out.Events, err
}

// post sends a JSON body and optionally decodes a JSON response. A 2xx status is
// success; anything else is an error carrying the daemon's message.
func (c *Client) post(path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}
	resp, err := c.http.Post("http://unix"+path, "application/json", &buf)
	if err != nil {
		return fmt.Errorf("connect to daemon at %s (is agent-taskd running?): %w", c.socket, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error != "" {
			return fmt.Errorf("POST %s: %s", path, e.Error)
		}
		return fmt.Errorf("POST %s: daemon returned %s", path, resp.Status)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
	}
	return nil
}

func (c *Client) get(path string, out any) error {
	resp, err := c.http.Get("http://unix" + path)
	if err != nil {
		return fmt.Errorf("connect to daemon at %s (is agent-taskd running?): %w", c.socket, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: daemon returned %s", path, resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}

	return nil
}
