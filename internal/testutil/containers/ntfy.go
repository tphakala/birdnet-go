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
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ntfyContainerPort is the default port exposed by the ntfy container.
const ntfyContainerPort = "80/tcp"

// NtfyContainer wraps a testcontainers ntfy push notification server instance.
type NtfyContainer struct {
	container  testcontainers.Container
	host       string
	port       int
	authEnabled bool
}

// NtfyConfig holds configuration for ntfy container creation.
type NtfyConfig struct {
	// ImageTag for binwiederhier/ntfy (default: "latest")
	ImageTag string
	// EnableAuth enables authentication with deny-all default access
	EnableAuth bool
}

// DefaultNtfyConfig returns an NtfyConfig with sensible defaults.
func DefaultNtfyConfig() NtfyConfig {
	return NtfyConfig{
		ImageTag:   "latest",
		EnableAuth: false,
	}
}

// NtfyMessage represents a message received from an ntfy topic.
type NtfyMessage struct {
	ID      string `json:"id"`
	Topic   string `json:"topic"`
	Message string `json:"message"`
	Title   string `json:"title"`
	Time    int64  `json:"time"`
}

// NewNtfyContainer creates and starts an ntfy push notification server container.
// If config is nil, uses DefaultNtfyConfig().
func NewNtfyContainer(ctx context.Context, config *NtfyConfig) (*NtfyContainer, error) {
	if config == nil {
		defaultCfg := DefaultNtfyConfig()
		config = &defaultCfg
	}

	image := fmt.Sprintf("binwiederhier/ntfy:%s", config.ImageTag)

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{ntfyContainerPort},
		Cmd:          []string{"serve", "--cache-file=/tmp/ntfy/cache.db"},
		Tmpfs:        map[string]string{"/tmp/ntfy": "rw"},
		WaitingFor: wait.ForHTTP("/v1/health").
			WithPort("80/tcp").
			WithStartupTimeout(30 * time.Second),
	}

	// Configure authentication if enabled
	if config.EnableAuth {
		req.Env = map[string]string{
			"NTFY_AUTH_FILE":           "/tmp/ntfy/auth.db",
			"NTFY_AUTH_DEFAULT_ACCESS": "deny-all",
		}
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start ntfy container: %w", err)
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

	nc := &NtfyContainer{
		container:   container,
		host:        host,
		port:        mappedPort.Int(),
		authEnabled: config.EnableAuth,
	}

	return nc, nil
}

// GetHost returns the host:port string where the ntfy server is accessible.
func (c *NtfyContainer) GetHost(_ context.Context) string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.port))
}

// GetURL returns the full HTTP URL for the ntfy server.
func (c *NtfyContainer) GetURL(_ context.Context) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(c.host, strconv.Itoa(c.port)))
}

// AddUser creates a new regular user in the ntfy container.
// Regular users have no default access when auth-default-access is deny-all;
// use GrantAccess to give them topic-specific permissions.
// This is only valid when authentication is enabled.
func (c *NtfyContainer) AddUser(ctx context.Context, username, password string) error {
	if !c.authEnabled {
		return fmt.Errorf("cannot add user: authentication is not enabled")
	}

	exitCode, output, err := c.container.Exec(ctx, []string{
		"ntfy", "user", "add", username,
	}, tcexec.WithEnv([]string{fmt.Sprintf("NTFY_PASSWORD=%s", password)}))
	if err != nil {
		return fmt.Errorf("failed to exec user add command: %w", err)
	}

	if exitCode != 0 {
		outputBytes, _ := io.ReadAll(output)
		return fmt.Errorf("ntfy user add failed with exit code %d: %s", exitCode, string(outputBytes))
	}

	return nil
}

// GrantAccess grants a user access to a specific topic with the given permission.
// Permission should be "ro" (read-only), "wo" (write-only), or "rw" (read-write).
// This is only valid when authentication is enabled.
func (c *NtfyContainer) GrantAccess(ctx context.Context, username, topic, permission string) error {
	if !c.authEnabled {
		return fmt.Errorf("cannot grant access: authentication is not enabled")
	}

	exitCode, output, err := c.container.Exec(ctx, []string{
		"ntfy", "access", username, topic, permission,
	})
	if err != nil {
		return fmt.Errorf("failed to exec access command: %w", err)
	}

	if exitCode != 0 {
		outputBytes, _ := io.ReadAll(output)
		return fmt.Errorf("ntfy access failed with exit code %d: %s", exitCode, string(outputBytes))
	}

	return nil
}

// PollMessages retrieves all cached messages from a topic using poll mode.
// This does not use authentication.
func (c *NtfyContainer) PollMessages(ctx context.Context, topic string) ([]NtfyMessage, error) {
	url := fmt.Sprintf("%s/%s/json?poll=1", c.GetURL(ctx), topic)
	return c.fetchMessages(ctx, url, "", "")
}

// PollMessagesWithAuth retrieves all cached messages from a topic using poll mode with Basic Auth.
func (c *NtfyContainer) PollMessagesWithAuth(ctx context.Context, topic, username, password string) ([]NtfyMessage, error) {
	url := fmt.Sprintf("%s/%s/json?poll=1", c.GetURL(ctx), topic)
	return c.fetchMessages(ctx, url, username, password)
}

// fetchMessages performs the HTTP request to retrieve messages from ntfy.
func (c *NtfyContainer) fetchMessages(ctx context.Context, url, username, password string) ([]NtfyMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if username != "" {
		req.SetBasicAuth(username, password)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to poll messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("poll request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// ntfy returns newline-delimited JSON (one message per line)
	var messages []NtfyMessage

	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg NtfyMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, fmt.Errorf("failed to parse message JSON: %w", err)
		}

		// Skip keepalive/open events that don't have a message
		if msg.Message == "" && msg.ID == "" {
			continue
		}

		messages = append(messages, msg)
	}

	return messages, nil
}

// HealthCheck performs a health check on the ntfy server by pinging /v1/health.
func (c *NtfyContainer) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/health", c.GetURL(ctx))

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

// Terminate stops and removes the ntfy container.
func (c *NtfyContainer) Terminate(ctx context.Context) error {
	if c.container != nil {
		if err := c.container.Terminate(ctx); err != nil {
			return fmt.Errorf("failed to terminate container: %w", err)
		}
	}
	return nil
}
