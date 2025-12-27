// mqtt.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Timeout constants for MQTT operations
const (
	// GracefulDisconnectTimeout is the timeout for graceful disconnect operations
	GracefulDisconnectTimeout = 5 * time.Second
	// CancelDisconnectTimeout is the timeout for disconnect during cancellation/timeout scenarios
	CancelDisconnectTimeout = 1 * time.Second
	// ShutdownDisconnectTimeout is the timeout for disconnect during application shutdown
	// This is shorter than graceful timeout to avoid delaying shutdown
	ShutdownDisconnectTimeout = 2 * time.Second
	// ConnectTimeoutGrace is the additional time to wait beyond ConnectTimeout for cleanup
	ConnectTimeoutGrace = 500 * time.Millisecond
	// KeepAliveInterval is the MQTT keep-alive interval
	KeepAliveInterval = 30 * time.Second
	// PingTimeout is the timeout for MQTT ping responses
	PingTimeout = 10 * time.Second
	// WriteTimeout is the timeout for MQTT write operations
	WriteTimeout = 10 * time.Second
	// DNSLookupTimeout is the timeout for DNS resolution during connection
	DNSLookupTimeout = 5 * time.Second
	// MinConnectTimeout is the minimum allowed connect timeout
	MinConnectTimeout = 500 * time.Millisecond
	// ReconnectContextGrace is the additional time beyond ConnectTimeout for reconnect context
	ReconnectContextGrace = 10 * time.Second
)

// durationToMillisUint safely converts a time.Duration to uint milliseconds.
// Returns 0 for negative durations. This prevents integer overflow when
// converting int64 milliseconds to uint (gosec G115).
func durationToMillisUint(d time.Duration) uint {
	ms := d.Milliseconds()
	if ms < 0 {
		return 0
	}
	return uint(ms) // #nosec G115 -- checked for negative values
}

// Client defines the interface for MQTT client operations.
type Client interface {
	// Connect attempts to connect to the MQTT broker.
	// It returns an error if the connection fails.
	Connect(ctx context.Context) error

	// Publish sends a message to the specified topic on the MQTT broker.
	// It returns an error if the publish operation fails.
	Publish(ctx context.Context, topic string, payload string) error

	// IsConnected returns true if the client is currently connected to the MQTT broker.
	IsConnected() bool

	// Disconnect closes the connection to the MQTT broker.
	Disconnect()

	// TestConnection performs a multi-stage test of the MQTT connection and functionality.
	// It streams test results through the provided channel.
	TestConnection(ctx context.Context, resultChan chan<- TestResult)

	// SetControlChannel sets the control channel for the client.
	// This channel is used to send control signals to the MQTT service.
	SetControlChannel(ch chan string)
}

// Config holds the configuration for the MQTT client.
type Config struct {
	Broker            string
	Debug             bool
	ClientID          string
	Username          string
	Password          string
	Topic             string // Default topic for publishing messages
	Retain            bool   // true to retain messages at the broker
	ReconnectCooldown time.Duration
	ReconnectDelay    time.Duration
	// Connection timeouts
	ConnectTimeout            time.Duration
	PublishTimeout            time.Duration
	DisconnectTimeout         time.Duration
	ShutdownDisconnectTimeout time.Duration // Timeout for disconnect during shutdown (shorter than normal)
	// TLS configuration
	TLS TLSConfig
}

// TLSConfig holds TLS/SSL configuration for secure MQTT connections
type TLSConfig struct {
	Enabled            bool   // true to enable TLS (auto-detected from broker URL)
	InsecureSkipVerify bool   // true to skip certificate verification
	CACert             string // path to CA certificate file
	ClientCert         string // path to client certificate file
	ClientKey          string // path to client key file
}

// GetLogger returns the module-scoped logger for MQTT operations.
// This logger is automatically integrated with the application's central logging system.
func GetLogger() logger.Logger {
	return logger.Global().Module("mqtt")
}

// DefaultConfig returns a Config with reasonable default values
func DefaultConfig() Config {
	return Config{
		ReconnectCooldown:         5 * time.Second,
		ReconnectDelay:            1 * time.Second,
		ConnectTimeout:            30 * time.Second,
		PublishTimeout:            10 * time.Second,
		DisconnectTimeout:         GracefulDisconnectTimeout, // Use constant for consistency
		ShutdownDisconnectTimeout: ShutdownDisconnectTimeout, // Shorter timeout for shutdown
	}
}
