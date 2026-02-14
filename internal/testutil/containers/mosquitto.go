//go:build integration

//nolint:misspell // Mosquitto is the official Eclipse project name
package containers

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MosquittoContainer wraps a testcontainers Eclipse Mosquitto MQTT broker instance.
type MosquittoContainer struct {
	container  testcontainers.Container
	brokerURL  string
	host       string
	port       int
	configFile string // Temp config file path for cleanup
}

// MosquittoConfig holds configuration for Mosquitto container creation.
type MosquittoConfig struct {
	// Image tag (default: "2.0")
	ImageTag string
	// Config file path (optional - for custom mosquitto.conf)
	ConfigFile string
	// Enable authentication (default: false, allows anonymous)
	EnableAuth bool
	// Username for authentication (if EnableAuth is true)
	Username string
	// Password for authentication (if EnableAuth is true)
	Password string
}

// DefaultMosquittoConfig returns a MosquittoConfig with sensible defaults.
func DefaultMosquittoConfig() MosquittoConfig {
	return MosquittoConfig{
		ImageTag:   "2.0",
		EnableAuth: false,
	}
}

// NewMosquittoContainer creates and starts a Mosquitto MQTT broker container.
// If config is nil, uses DefaultMosquittoConfig().
func NewMosquittoContainer(config *MosquittoConfig) (*MosquittoContainer, error) {
	if config == nil {
		defaultCfg := DefaultMosquittoConfig()
		config = &defaultCfg
	}

	ctx := context.Background()

	image := fmt.Sprintf("eclipse-mosquitto:%s", config.ImageTag)

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{"1883/tcp"},
		WaitingFor: wait.ForLog("mosquitto version").
			WithStartupTimeout(30 * time.Second),
	}

	var configFile string
	var err error

	// Configure mosquitto to allow anonymous connections by default
	// This is done via command override
	if !config.EnableAuth {
		// Create a simple mosquitto.conf that allows anonymous connections
		configFile, err = createTempMosquittoConfig(false)
		if err != nil {
			return nil, fmt.Errorf("failed to create mosquitto config: %w", err)
		}

		req.Cmd = []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"}
		req.Files = []testcontainers.ContainerFile{
			{
				HostFilePath:      configFile,
				ContainerFilePath: "/mosquitto-no-auth.conf",
				FileMode:          0o644,
			},
		}
	} else {
		// TODO: Add authentication support if needed
		// This would require creating a password file and custom config
		return nil, fmt.Errorf("authentication not yet implemented")
	}

	// Note: Container reuse is not reliably supported for Mosquitto at this time.
	// Containers are created fresh for each test run to ensure isolation.

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		// Clean up temp config file on error
		if configFile != "" {
			_ = os.Remove(configFile)
		}
		return nil, fmt.Errorf("failed to start Mosquitto container: %w", err)
	}

	// Get host and port
	host, err := container.Host(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		if configFile != "" {
			_ = os.Remove(configFile)
		}
		return nil, fmt.Errorf("failed to get container host: %w", err)
	}

	mappedPort, err := container.MappedPort(ctx, "1883")
	if err != nil {
		_ = container.Terminate(ctx)
		if configFile != "" {
			_ = os.Remove(configFile)
		}
		return nil, fmt.Errorf("failed to get mapped port: %w", err)
	}

	port := mappedPort.Int()
	brokerURL := fmt.Sprintf("tcp://%s", net.JoinHostPort(host, strconv.Itoa(port)))

	mc := &MosquittoContainer{
		container:  container,
		brokerURL:  brokerURL,
		host:       host,
		port:       port,
		configFile: configFile,
	}

	// Verify broker is ready with health check
	if err := mc.HealthCheck(ctx); err != nil {
		_ = container.Terminate(ctx)
		if configFile != "" {
			_ = os.Remove(configFile)
		}
		return nil, fmt.Errorf("health check failed: %w", err)
	}

	return mc, nil
}

// createTempMosquittoConfig creates a temporary mosquitto configuration file.
// Returns the file path and an error if creation fails.
// The caller is responsible for cleaning up the temporary file.
func createTempMosquittoConfig(enableAuth bool) (string, error) {
	configContent := `# Mosquitto test configuration
listener 1883
allow_anonymous true
`
	if enableAuth {
		configContent = `# Mosquitto test configuration with auth
listener 1883
allow_anonymous false
password_file /mosquitto/config/passwd
`
	}

	tmpFile, err := os.CreateTemp("", "mosquitto-*.conf")
	if err != nil {
		return "", fmt.Errorf("failed to create temp config: %w", err)
	}

	if _, err := tmpFile.WriteString(configContent); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to write config: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to close temp config: %w", err)
	}

	return tmpFile.Name(), nil
}

// GetBrokerURL returns the MQTT broker URL (e.g., "tcp://localhost:1883").
func (c *MosquittoContainer) GetBrokerURL(t *testing.T) string {
	t.Helper()
	if c.brokerURL == "" {
		t.Fatal("broker URL is empty")
	}
	return c.brokerURL
}

// GetHost returns the host address where the broker is accessible.
func (c *MosquittoContainer) GetHost() string {
	return c.host
}

// GetPort returns the mapped port where MQTT is accessible.
func (c *MosquittoContainer) GetPort() int {
	return c.port
}

// HealthCheck performs a health check on the MQTT broker by connecting and disconnecting.
func (c *MosquittoContainer) HealthCheck(ctx context.Context) error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.brokerURL)
	opts.SetClientID("healthcheck")
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetAutoReconnect(false)

	client := mqtt.NewClient(opts)
	token := client.Connect()

	// Wait for connection with timeout (avoids goroutine leak)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("health check timeout after 5s")
	}
	if token.Error() != nil {
		return fmt.Errorf("failed to connect to broker: %w", token.Error())
	}

	// Disconnect
	client.Disconnect(250)
	return nil
}

// CreateClient creates a new MQTT client connected to this broker.
// The caller is responsible for disconnecting the client when done.
func (c *MosquittoContainer) CreateClient(clientID string, opts ...func(*mqtt.ClientOptions)) (mqtt.Client, error) {
	mqttOpts := mqtt.NewClientOptions()
	mqttOpts.AddBroker(c.brokerURL)
	mqttOpts.SetClientID(clientID)
	mqttOpts.SetConnectTimeout(10 * time.Second)
	mqttOpts.SetAutoReconnect(true)

	// Apply additional options
	for _, opt := range opts {
		opt(mqttOpts)
	}

	client := mqtt.NewClient(mqttOpts)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("connect timeout for client %s", clientID)
	}
	if token.Error() != nil {
		return nil, fmt.Errorf("failed to connect client: %w", token.Error())
	}

	return client, nil
}

// ClearRetainedMessages clears all retained messages from the broker.
// This is done by subscribing to # and publishing empty messages to each retained topic.
func (c *MosquittoContainer) ClearRetainedMessages(ctx context.Context) error {
	client, err := c.CreateClient("cleaner")
	if err != nil {
		return fmt.Errorf("failed to create cleaner client: %w", err)
	}
	defer client.Disconnect(250)

	var mu sync.Mutex
	retainedTopics := make([]string, 0)
	done := make(chan bool, 1)

	// Subscribe to all topics to find retained messages
	token := client.Subscribe("#", 0, func(client mqtt.Client, msg mqtt.Message) {
		if msg.Retained() {
			mu.Lock()
			retainedTopics = append(retainedTopics, msg.Topic())
			mu.Unlock()
		}
	})
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("subscribe timeout after 5s")
	}
	if token.Error() != nil {
		return fmt.Errorf("failed to subscribe: %w", token.Error())
	}

	// Wait for retained messages with timeout
	go func() {
		time.Sleep(100 * time.Millisecond)
		done <- true
	}()

	select {
	case <-done:
		// Timeout reached
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while waiting for retained messages: %w", ctx.Err())
	}

	// Unsubscribe
	unsubToken := client.Unsubscribe("#")
	if !unsubToken.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("unsubscribe timeout after 5s")
	}
	if unsubToken.Error() != nil {
		return fmt.Errorf("failed to unsubscribe: %w", unsubToken.Error())
	}

	// Clear retained messages by publishing empty payloads
	mu.Lock()
	topicsCopy := make([]string, len(retainedTopics))
	copy(topicsCopy, retainedTopics)
	mu.Unlock()

	for _, topic := range topicsCopy {
		token := client.Publish(topic, 0, true, nil)
		if !token.WaitTimeout(5 * time.Second) {
			return fmt.Errorf("publish timeout for topic %s after 5s", topic)
		}
		if token.Error() != nil {
			return fmt.Errorf("failed to clear topic %s: %w", topic, token.Error())
		}
	}

	return nil
}

// Terminate stops and removes the Mosquitto container.
// Also cleans up the temporary config file if one was created.
func (c *MosquittoContainer) Terminate() error {
	var terminateErr error

	// Terminate container
	if c.container != nil {
		ctx := context.Background()
		if err := c.container.Terminate(ctx); err != nil {
			terminateErr = fmt.Errorf("failed to terminate container: %w", err)
		}
	}

	// Clean up temp config file
	if c.configFile != "" {
		if err := os.Remove(c.configFile); err != nil && !os.IsNotExist(err) {
			// Log warning but don't override container termination error
			fmt.Printf("Warning: failed to remove temp config file %s: %v\n", c.configFile, err)
		}
	}

	return terminateErr
}
