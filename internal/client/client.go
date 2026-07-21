package client

import (
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
