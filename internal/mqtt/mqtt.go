// mqtt.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/logging"
)

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
	ClientID          string
	Username          string
	Password          string
	Topic             string // Default topic for publishing messages
	Retain            bool   // true to retain messages at the broker
	ReconnectCooldown time.Duration
	ReconnectDelay    time.Duration
	// Connection timeouts
	ConnectTimeout    time.Duration
	PublishTimeout    time.Duration
	DisconnectTimeout time.Duration
}

// Package-level logger for MQTT related events
var (
	mqttLogger *slog.Logger
	// mqttLogCloser func() error // Optional closer func
	// TODO: Call mqttLogCloser during graceful shutdown if needed
)

func init() {
	var err error
	// Default level is Info. MQTT interactions might benefit from Debug level
	// during troubleshooting, but Info is a good default.
	mqttLogger, _, err = logging.NewFileLogger("logs/mqtt.log", "mqtt", slog.LevelInfo)
	if err != nil {
		logging.Error("Failed to initialize MQTT file logger", "error", err)
		// Fallback to the default structured logger
		mqttLogger = logging.Structured().With("service", "mqtt")
		if mqttLogger == nil {
			panic(fmt.Sprintf("Failed to initialize any logger for MQTT service: %v", err))
		}
		logging.Warn("MQTT service falling back to default logger due to file logger initialization error.")
	} else {
		logging.Info("MQTT file logger initialized successfully", "path", "logs/mqtt.log")
	}
	// mqttLogCloser = closer
}

// DefaultConfig returns a Config with reasonable default values
func DefaultConfig() Config {
	return Config{
		ReconnectCooldown: 5 * time.Second,
		ReconnectDelay:    1 * time.Second,
		ConnectTimeout:    30 * time.Second,
		PublishTimeout:    10 * time.Second,
		DisconnectTimeout: 250 * time.Millisecond,
	}
}
