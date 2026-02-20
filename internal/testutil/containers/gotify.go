//go:build integration

package containers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// gotifyContainerPort is the default port exposed by the Gotify container.
const gotifyContainerPort = "80/tcp"

// GotifyContainer wraps a testcontainers Gotify push notification server instance.
type GotifyContainer struct {
	container testcontainers.Container
	host      string
	port      int
}

// GotifyConfig holds configuration for Gotify container creation.
type GotifyConfig struct {
	// ImageTag for gotify/server (default: "latest")
	ImageTag string
	// DefaultUserPass sets the admin password (default: "admin")
	DefaultUserPass string
}

// DefaultGotifyConfig returns a GotifyConfig with sensible defaults.
func DefaultGotifyConfig() GotifyConfig {
	return GotifyConfig{
		ImageTag:        "latest",
		DefaultUserPass: "admin",
	}
}

// GotifyApplication represents an application created via the Gotify API.
type GotifyApplication struct {
	ID          int    `json:"id"`
	Token       string `json:"token"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GotifyMessage represents a message received from the Gotify API.
type GotifyMessage struct {
	ID       int            `json:"id"`
	AppID    int            `json:"appid"`
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Date     string         `json:"date"`
	Extras   map[string]any `json:"extras"`
}

// GotifyPagedMessages represents the paginated message response from Gotify.
type GotifyPagedMessages struct {
	Messages []GotifyMessage `json:"messages"`
	Paging   struct {
		Size  int    `json:"size"`
		Limit int    `json:"limit"`
		Since int    `json:"since"`
		Next  string `json:"next"`
	} `json:"paging"`
}

// NewGotifyContainer creates and starts a Gotify push notification server container.
// If config is nil, uses DefaultGotifyConfig().
func NewGotifyContainer(ctx context.Context, config *GotifyConfig) (*GotifyContainer, error) {
	if config == nil {
		defaultCfg := DefaultGotifyConfig()
		config = &defaultCfg
	}

	image := fmt.Sprintf("gotify/server:%s", config.ImageTag)

	env := map[string]string{
		"GOTIFY_DEFAULTUSER_PASS": config.DefaultUserPass,
	}

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{gotifyContainerPort},
		Env:          env,
		WaitingFor: wait.ForHTTP("/health").
			WithPort("80/tcp").
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start gotify container: %w", err)
	}

	// Get host and port
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "80")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	gc := &GotifyContainer{
		container: container,
		host:      host,
		port:      mappedPort.Int(),
	}

	return gc, nil
}

// GetHost returns the host:port string where the Gotify server is accessible.
func (c *GotifyContainer) GetHost(_ context.Context) string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// GetURL returns the full HTTP URL for the Gotify server.
func (c *GotifyContainer) GetURL(_ context.Context) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(c.host, strconv.Itoa(c.port)))
}

// CreateApplication creates a new application in Gotify and returns the application details
// including the token needed for sending messages.
func (c *GotifyContainer) CreateApplication(ctx context.Context, name, description string) (*GotifyApplication, error) {
	url := fmt.Sprintf("%s/application", c.GetURL(ctx))

	body := fmt.Sprintf(`{"name":%q,"description":%q}`, name, description)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "admin")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create application failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var app GotifyApplication
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode application response: %w", err)
	}

	return &app, nil
}

// GetMessages retrieves all messages for a specific application.
func (c *GotifyContainer) GetMessages(ctx context.Context, appID int) ([]GotifyMessage, error) {
	url := fmt.Sprintf("%s/application/%d/message", c.GetURL(ctx), appID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth("admin", "admin")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get messages failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var paged GotifyPagedMessages
	if err := json.NewDecoder(resp.Body).Decode(&paged); err != nil {
		return nil, fmt.Errorf("failed to decode messages response: %w", err)
	}

	return paged.Messages, nil
}

// GetAllMessages retrieves all messages across all applications.
func (c *GotifyContainer) GetAllMessages(ctx context.Context) ([]GotifyMessage, error) {
	url := fmt.Sprintf("%s/message", c.GetURL(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.SetBasicAuth("admin", "admin")

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get messages failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var paged GotifyPagedMessages
	if err := json.NewDecoder(resp.Body).Decode(&paged); err != nil {
		return nil, fmt.Errorf("failed to decode messages response: %w", err)
	}

	return paged.Messages, nil
}

// HealthCheck performs a health check on the Gotify server by pinging /health.
func (c *GotifyContainer) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", c.GetURL(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}

	return nil
}

// Terminate stops and removes the Gotify container.
func (c *GotifyContainer) Terminate(ctx context.Context) error {
	if c.container != nil {
		if err := c.container.Terminate(ctx); err != nil {
			return fmt.Errorf("failed to terminate container: %w", err)
		}
	}
	return nil
}
