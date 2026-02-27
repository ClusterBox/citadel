package docker

import (
	"context"
	"fmt"

	"github.com/docker/docker/client"
)

// Client wraps Docker client
type Client struct {
	Docker *client.Client
}

// NewClient creates a new Docker client
func NewClient(ctx context.Context) (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	return &Client{Docker: cli}, nil
}

// Close closes the Docker client
func (c *Client) Close() error {
	return c.Docker.Close()
}
