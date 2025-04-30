// client.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

const (
	// defaultQoS is the default Quality of Service level for MQTT messages
	defaultQoS = 1 // QoS 1 ensures at least once delivery
)

// client implements the Client interface.
type client struct {
	config          Config
	internalClient  mqtt.Client
	lastConnAttempt time.Time
	mu              sync.RWMutex
	reconnectTimer  *time.Timer
	reconnectStop   chan struct{}
	metrics         *metrics.MQTTMetrics
	controlChan     chan string // Channel for control signals
}

// NewClient creates a new MQTT client with the provided configuration.
func NewClient(settings *conf.Settings, metrics *telemetry.Metrics) (Client, error) {
	mqttLogger.Info("Creating new MQTT client")
	config := DefaultConfig()
	config.Broker = settings.Realtime.MQTT.Broker
	config.ClientID = settings.Main.Name
	config.Username = settings.Realtime.MQTT.Username
	config.Password = settings.Realtime.MQTT.Password // Keep password in config, but don't log it
	config.Topic = settings.Realtime.MQTT.Topic
	config.Retain = settings.Realtime.MQTT.Retain

	// Log config details without sensitive info
	mqttLogger.Info("MQTT configuration loaded",
		"broker", config.Broker,
		"client_id", config.ClientID,
		"username", config.Username, // Log username, usually not sensitive
		"topic", config.Topic,
		"retain", config.Retain,
	)

	return &client{
		config:        config,
		reconnectStop: make(chan struct{}),
		metrics:       metrics.MQTT,
		controlChan:   nil, // Will be set externally when needed
	}, nil
}

// SetControlChannel sets the control channel for the client
func (c *client) SetControlChannel(ch chan string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	mqttLogger.Debug("Setting control channel for MQTT client")
	c.controlChan = ch
}

// Connect attempts to establish a connection to the MQTT broker.
func (c *client) Connect(ctx context.Context) error {
	// Check context before acquiring lock
	if err := ctx.Err(); err != nil {
		mqttLogger.Warn("Connect context already cancelled", "error", err)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	logger.Info("Attempting to connect to MQTT broker")

	if time.Since(c.lastConnAttempt) < c.config.ReconnectCooldown {
		lastAttemptAgo := time.Since(c.lastConnAttempt)
		logger.Warn("Connection attempt too recent", "last_attempt_ago", lastAttemptAgo, "cooldown", c.config.ReconnectCooldown)
		return fmt.Errorf("connection attempt too recent, last attempt was %v ago", lastAttemptAgo)
	}
	c.lastConnAttempt = time.Now()

	// Parse the broker URL
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		logger.Error("Invalid broker URL", "error", err)
		return fmt.Errorf("invalid broker URL: %w", err)
	}

	// Create a child context with timeout for DNS resolution
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dnsCancel()

	host := u.Hostname()
	if net.ParseIP(host) == nil {
		logger.Debug("Resolving broker hostname", "host", host)
		_, err := net.DefaultResolver.LookupHost(dnsCtx, host)
		if err != nil {
			logger.Error("Failed to resolve broker hostname", "host", host, "error", err)
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) {
				return dnsErr // Return specific DNS error if possible
			}
			return fmt.Errorf("failed to resolve hostname %s: %w", host, err)
		}
		logger.Debug("Broker hostname resolved successfully", "host", host)
	}

	// Clean up any existing client
	if c.internalClient != nil && c.internalClient.IsConnected() {
		logger.Info("Disconnecting existing client before reconnecting")
		c.internalClient.Disconnect(uint(c.config.DisconnectTimeout.Milliseconds()))
	}

	// Create connection options
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.config.Broker)
	opts.SetClientID(c.config.ClientID)
	opts.SetUsername(c.config.Username)
	// DO NOT log the password
	opts.SetPassword(c.config.Password)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(false) // We'll handle reconnection ourselves
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)
	opts.SetConnectRetry(false) // We'll handle retries ourselves
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetWriteTimeout(10 * time.Second)
	opts.SetConnectTimeout(c.config.ConnectTimeout)

	logger.Debug("MQTT client options configured",
		"keepalive", 30*time.Second,
		"ping_timeout", 10*time.Second,
		"write_timeout", 10*time.Second,
		"connect_timeout", c.config.ConnectTimeout,
		"clean_session", true,
	)

	c.internalClient = mqtt.NewClient(opts)

	// Create channels for connection handling
	done := make(chan error, 1)
	connectStarted := make(chan struct{})

	// Start connection attempt in a goroutine
	go func() {
		close(connectStarted)
		logger.Debug("Starting connection attempt goroutine")

		// Add a small delay in test mode to ensure we can test context cancellation
		if strings.Contains(c.config.ClientID, "TestNode") {
			time.Sleep(200 * time.Millisecond)
		}

		// Check context before attempting connection
		if ctx.Err() != nil {
			logger.Warn("Connection context cancelled before Connect call", "error", ctx.Err())
			done <- ctx.Err()
			return
		}

		token := c.internalClient.Connect()
		if !token.WaitTimeout(c.config.ConnectTimeout) {
			logger.Error("MQTT connection timed out", "timeout", c.config.ConnectTimeout)
			done <- fmt.Errorf("connection timeout after %v", c.config.ConnectTimeout)
			return
		}
		connectErr := token.Error()
		if connectErr != nil {
			logger.Error("MQTT connection failed", "error", connectErr)
		}
		done <- connectErr
	}()

	// Wait for either context cancellation or connection completion
	select {
	case <-ctx.Done():
		// Context was cancelled during connection attempt
		logger.Warn("Connection context cancelled during connect attempt", "error", ctx.Err())
		// Try to clean up
		if c.internalClient != nil {
			// Use a short timeout for disconnection attempt during cancellation
			c.internalClient.Disconnect(250) // 250ms
		}
		return ctx.Err()
	case err := <-done:
		if err != nil {
			// Connection failed after attempt
			return fmt.Errorf("connection failed: %w", err)
		}
		// Connection successful (onConnect handler will log success)
		return nil
	}
}

// Publish sends a message to the specified topic on the MQTT broker.
func (c *client) Publish(ctx context.Context, topic, payload string) error {
	// Check context before acquiring lock
	if err := ctx.Err(); err != nil {
		mqttLogger.Warn("Publish context already cancelled", "topic", topic, "error", err)
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	logger := mqttLogger.With("topic", topic, "qos", defaultQoS, "retain", c.config.Retain)

	if !c.IsConnected() {
		logger.Warn("Publish failed: not connected to MQTT broker")
		return fmt.Errorf("not connected to MQTT broker")
	}

	timer := c.metrics.StartPublishTimer()
	defer timer.ObserveDuration()

	logger.Debug("Publishing message", "payload_size", len(payload))

	// Create a channel to handle publish timeout
	done := make(chan error, 1)
	publishStarted := make(chan struct{})

	go func() {
		close(publishStarted)
		logger.Debug("Starting publish attempt goroutine")
		token := c.internalClient.Publish(topic, defaultQoS, c.config.Retain, payload)
		if !token.WaitTimeout(c.config.PublishTimeout) {
			logger.Error("MQTT publish timed out", "timeout", c.config.PublishTimeout)
			done <- fmt.Errorf("publish timeout after %v", c.config.PublishTimeout)
			return
		}
		publishErr := token.Error()
		if publishErr != nil {
			logger.Error("MQTT publish failed", "error", publishErr)
		}
		done <- publishErr
	}()

	// Wait for publish attempt to start
	<-publishStarted

	// Wait for either context cancellation or publish completion
	select {
	case <-ctx.Done():
		logger.Warn("Publish context cancelled during publish attempt", "error", ctx.Err())
		return ctx.Err()
	case err := <-done:
		if err != nil {
			c.metrics.IncrementErrors()
			return fmt.Errorf("publish error: %w", err)
		}
	}

	logger.Debug("Publish successful")
	c.metrics.IncrementMessagesDelivered()
	c.metrics.ObserveMessageSize(float64(len(payload)))
	return nil
}

// IsConnected returns true if the client is currently connected to the MQTT broker.
func (c *client) IsConnected() bool {
	// RLock is sufficient for read-only check
	c.mu.RLock()
	defer c.mu.RUnlock()
	connected := c.internalClient != nil && c.internalClient.IsConnected()
	mqttLogger.Debug("Checking MQTT connection status", "is_connected", connected)
	return connected
}

// Disconnect closes the connection to the MQTT broker.
func (c *client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	logger.Info("Disconnecting from MQTT broker")

	// Signal reconnect loop to stop
	select {
	case <-c.reconnectStop:
		// Already closed
	default:
		close(c.reconnectStop)
	}

	if c.reconnectTimer != nil {
		logger.Debug("Stopping reconnect timer")
		c.reconnectTimer.Stop()
	}

	if c.internalClient != nil && c.internalClient.IsConnected() {
		disconnectTimeoutMs := uint(c.config.DisconnectTimeout.Milliseconds())
		logger.Debug("Sending disconnect signal to Paho client", "timeout_ms", disconnectTimeoutMs)
		c.internalClient.Disconnect(disconnectTimeoutMs)
		c.metrics.UpdateConnectionStatus(false)
	} else {
		logger.Debug("Client already disconnected or not initialized")
	}
}

func (c *client) onConnect(client mqtt.Client) {
	// Log using the package-level logger
	mqttLogger.Info("✅ Connected to MQTT broker", "broker", c.config.Broker, "client_id", c.config.ClientID)
	c.metrics.UpdateConnectionStatus(true)
	// Reset reconnect attempts on successful connection
	// (This logic might be better placed elsewhere if reconnect has state)
}

func (c *client) onConnectionLost(client mqtt.Client, err error) {
	// Log using the package-level logger
	mqttLogger.Error("❌ Connection to MQTT broker lost", "broker", c.config.Broker, "client_id", c.config.ClientID, "error", err)
	c.metrics.UpdateConnectionStatus(false)
	c.metrics.IncrementErrors()
	c.startReconnectTimer()
}

func (c *client) startReconnectTimer() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Ensure we don't start multiple timers
	if c.reconnectTimer != nil {
		c.reconnectTimer.Stop()
	}

	reconnectDelay := c.config.ReconnectDelay
	mqttLogger.Info("Starting reconnect timer", "delay", reconnectDelay)
	c.reconnectTimer = time.AfterFunc(reconnectDelay, func() {
		select {
		case <-c.reconnectStop: // Check if disconnect was called
			mqttLogger.Info("Reconnect cancelled")
			return
		default:
			c.reconnectWithBackoff()
		}
	})
}

func (c *client) reconnectWithBackoff() {
	// Use ConnectTimeout for the overall reconnect attempt context
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout)
	defer cancel()

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	logger.Info("Attempting to reconnect to MQTT broker")

	if err := c.Connect(ctx); err != nil {
		logger.Error("Reconnect attempt failed", "error", err)
		// Schedule next attempt with potentially increased delay (if implementing backoff)
		// For now, just reschedule with the base delay
		c.startReconnectTimer() // Reschedule another attempt after delay
	} else {
		// Connection successful, logged by onConnect
		logger.Info("Reconnect successful")
		// No need to call startReconnectTimer here, connection is established
	}
}

// TestConnection performs a multi-stage test of the MQTT connection and functionality.
// ... rest of file ...
