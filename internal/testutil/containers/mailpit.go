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
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// mailpitSMTPPort is the SMTP port exposed by Mailpit.
const mailpitSMTPPort = "1025/tcp"

// mailpitAPIPort is the HTTP API/UI port exposed by Mailpit.
const mailpitAPIPort = "8025/tcp"

// MailpitContainer wraps a testcontainers Mailpit SMTP testing server instance.
// Mailpit captures all incoming SMTP messages and provides a REST API to retrieve them.
type MailpitContainer struct {
	container testcontainers.Container
	host      string
	smtpPort  int
	apiPort   int
}

// MailpitConfig holds configuration for Mailpit container creation.
type MailpitConfig struct {
	// ImageTag for axllent/mailpit (default: "latest")
	ImageTag string
}

// DefaultMailpitConfig returns a MailpitConfig with sensible defaults.
func DefaultMailpitConfig() MailpitConfig {
	return MailpitConfig{
		ImageTag: "latest",
	}
}

// MailpitMessage represents an email captured by Mailpit.
type MailpitMessage struct {
	ID      string            `json:"ID"`
	From    MailpitAddress    `json:"From"`
	To      []MailpitAddress  `json:"To"`
	Subject string            `json:"Subject"`
	Snippet string            `json:"Snippet"`
	Created string            `json:"Created"`
	Tags    []string          `json:"Tags"`
	Size    int               `json:"Size"`
	Read    bool              `json:"Read"`
}

// MailpitAddress represents an email address in Mailpit.
type MailpitAddress struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// MailpitMessageDetail represents a full email message with body content.
type MailpitMessageDetail struct {
	ID      string           `json:"ID"`
	From    MailpitAddress   `json:"From"`
	To      []MailpitAddress `json:"To"`
	Subject string           `json:"Subject"`
	Text    string           `json:"Text"`
	HTML    string           `json:"HTML"`
	Created string           `json:"Created"`
}

// MailpitMessageList represents the paginated message list response from Mailpit.
type MailpitMessageList struct {
	Total         int              `json:"total"`
	Unread        int              `json:"unread"`
	Count         int              `json:"count"`
	MessagesCount int              `json:"messages_count"`
	Start         int              `json:"start"`
	Tags          []string         `json:"tags"`
	Messages      []MailpitMessage `json:"messages"`
}

// NewMailpitContainer creates and starts a Mailpit SMTP testing server container.
// If config is nil, uses DefaultMailpitConfig().
func NewMailpitContainer(ctx context.Context, config *MailpitConfig) (*MailpitContainer, error) {
	if config == nil {
		defaultCfg := DefaultMailpitConfig()
		config = &defaultCfg
	}

	image := fmt.Sprintf("axllent/mailpit:%s", config.ImageTag)

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{mailpitSMTPPort, mailpitAPIPort},
		WaitingFor: wait.ForHTTP("/api/v1/info").
			WithPort("8025/tcp").
			WithStartupTimeout(30 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start mailpit container: %w", err)
	}

	// Get host
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	// Get SMTP port
	smtpMapped, err := container.MappedPort(ctx, "1025")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get mapped SMTP port: %w", err)
	}

	// Get API port
	apiMapped, err := container.MappedPort(ctx, "8025")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("failed to get mapped API port: %w", err)
	}

	mc := &MailpitContainer{
		container: container,
		host:      host,
		smtpPort:  smtpMapped.Int(),
		apiPort:   apiMapped.Int(),
	}

	return mc, nil
}

// GetSMTPHost returns the host:port string for the SMTP server.
func (c *MailpitContainer) GetSMTPHost(_ context.Context) string {
	return net.JoinHostPort(c.host, strconv.Itoa(c.smtpPort))
}

// GetSMTPPort returns the mapped SMTP port.
func (c *MailpitContainer) GetSMTPPort() int {
	return c.smtpPort
}

// GetAPIURL returns the full HTTP URL for the Mailpit REST API.
func (c *MailpitContainer) GetAPIURL(_ context.Context) string {
	return fmt.Sprintf("http://%s", net.JoinHostPort(c.host, strconv.Itoa(c.apiPort)))
}

// GetHost returns the container host (without port).
func (c *MailpitContainer) GetHost() string {
	return c.host
}

// ListMessages retrieves all captured emails from Mailpit.
func (c *MailpitContainer) ListMessages(ctx context.Context) ([]MailpitMessage, error) {
	url := fmt.Sprintf("%s/api/v1/messages", c.GetAPIURL(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list messages failed with status %d: %s", resp.StatusCode, string(body))
	}

	var list MailpitMessageList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to decode messages response: %w", err)
	}

	return list.Messages, nil
}

// GetMessage retrieves the full details of a specific email by ID.
func (c *MailpitContainer) GetMessage(ctx context.Context, messageID string) (*MailpitMessageDetail, error) {
	url := fmt.Sprintf("%s/api/v1/message/%s", c.GetAPIURL(ctx), messageID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get message failed with status %d: %s", resp.StatusCode, string(body))
	}

	var detail MailpitMessageDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("failed to decode message detail: %w", err)
	}

	return &detail, nil
}

// DeleteAllMessages deletes all captured emails from Mailpit.
func (c *MailpitContainer) DeleteAllMessages(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/messages", c.GetAPIURL(ctx))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete messages failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// MessageCount returns the total number of captured emails.
func (c *MailpitContainer) MessageCount(ctx context.Context) (int, error) {
	messages, err := c.ListMessages(ctx)
	if err != nil {
		return 0, err
	}
	return len(messages), nil
}

// HealthCheck performs a health check on the Mailpit API server.
func (c *MailpitContainer) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/v1/info", c.GetAPIURL(ctx))

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

// Terminate stops and removes the Mailpit container.
func (c *MailpitContainer) Terminate(ctx context.Context) error {
	if c.container != nil {
		if err := c.container.Terminate(ctx); err != nil {
			return fmt.Errorf("failed to terminate container: %w", err)
		}
	}
	return nil
}
