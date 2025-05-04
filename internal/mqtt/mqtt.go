// mqtt.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"io"
	"log"
	"log/slog"
	"path/filepath"
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
	Debug             bool
	ClientID          string
	Username          string
	Password          string
	Topic             string // Default topic for publishing messages
	Retain            bool   // true to retain messages at the broker
	ReconnectCooldown time.Duration
	ReconnectDelay    time.Duration
	// Connection timeouts
	ConnectTimeout    time.Duration
	ReconnectTimeout  time.Duration
	PublishTimeout    time.Duration
	DisconnectTimeout time.Duration
}

// Package-level logger for MQTT related events
var (
	mqttLogger *slog.Logger
	// mqttLogWriter   io.Writer     // No longer needed directly
	mqttLogCloser   func() error         // Stores the closer function
	mqttLogFilePath string               // Stores the log file path
	mqttLevelVar    = new(slog.LevelVar) // Dynamic level control
	// mqttLogMutex    sync.RWMutex // No longer needed for level changes
)

func init() {
	mqttLogFilePath = filepath.Join("logs", "mqtt.log") // Use filepath.Join for safety
	initialLevel := slog.LevelInfo                      // Default level
	mqttLevelVar.Set(initialLevel)                      // Set initial level

	var err error
	// Initialize the service-specific file logger using the LevelVar
	mqttLogger, mqttLogCloser, err = logging.NewFileLogger(mqttLogFilePath, "mqtt", mqttLevelVar)
	if err != nil {
		// Use standard log for this critical setup error, as logging might not be fully functional
		log.Printf("ERROR: Failed to initialize MQTT file logger at %s: %v. Service logging disabled.", mqttLogFilePath, err)
		// Fallback to a disabled logger to prevent nil panics
		mqttLogger = slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: mqttLevelVar}))
		// mqttLogWriter = io.Discard // No longer needed
		mqttLogCloser = func() error { return nil } // No-op closer for fallback
	} else {
		// mqttLogWriter = writer // No longer needed
		// Use standard log for initial confirmation message (now uses slog)
		mqttLogger.Info("MQTT file logger initialised", "path", mqttLogFilePath, "level", initialLevel.String())
	}
}

// SetLogLevel dynamically changes the logging level for the MQTT logger.
func SetLogLevel(level slog.Level) {
	oldLevel := mqttLevelVar.Level()
	if level == oldLevel {
		mqttLogger.Debug("MQTT logger level already set", "level", level.String())
		return
	}

	mqttLogger.Info("Setting MQTT logger level", "old_level", oldLevel.String(), "new_level", level.String())
	mqttLevelVar.Set(level)
}

// CloseLogger closes the MQTT-specific file logger, if one was successfully initialized.
// This should be called during graceful shutdown.
func CloseLogger() error {
	if mqttLogCloser != nil {
		log.Println("Closing MQTT file logger...")
		return mqttLogCloser()
	}
	return nil
}

// DefaultConfig returns a Config with reasonable default values
func DefaultConfig() Config {
	return Config{
		ReconnectCooldown: 5 * time.Second,
		ReconnectDelay:    1 * time.Second,
		ConnectTimeout:    30 * time.Second,
		ReconnectTimeout:  5 * time.Second,
		PublishTimeout:    10 * time.Second,
		DisconnectTimeout: 250 * time.Millisecond,
	}
}
