// client.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
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
func NewClient(settings *conf.Settings, observabilityMetrics *observability.Metrics) (Client, error) {
	mqttLogger.Info("Creating new MQTT client")
	config := DefaultConfig()
	config.Broker = settings.Realtime.MQTT.Broker
	config.ClientID = settings.Main.Name
	config.Username = settings.Realtime.MQTT.Username
	config.Password = settings.Realtime.MQTT.Password // Keep password in config, but don't log it
	config.Topic = settings.Realtime.MQTT.Topic
	config.Retain = settings.Realtime.MQTT.Retain
	config.Debug = settings.Realtime.MQTT.Debug

	// Configure TLS settings
	config.TLS.Enabled = settings.Realtime.MQTT.TLS.Enabled
	config.TLS.InsecureSkipVerify = settings.Realtime.MQTT.TLS.InsecureSkipVerify
	config.TLS.CACert = settings.Realtime.MQTT.TLS.CACert
	config.TLS.ClientCert = settings.Realtime.MQTT.TLS.ClientCert
	config.TLS.ClientKey = settings.Realtime.MQTT.TLS.ClientKey

	// Auto-detect TLS from broker URL scheme
	if strings.HasPrefix(config.Broker, "ssl://") || strings.HasPrefix(config.Broker, "tls://") || strings.HasPrefix(config.Broker, "mqtts://") {
		config.TLS.Enabled = true
		mqttLogger.Info("TLS enabled based on broker URL scheme")
	}

	// Set log level based on the Debug flag
	if config.Debug {
		SetLogLevel(slog.LevelDebug)
		mqttLogger.Debug("MQTT Debug logging enabled") // Log that debug is on
	} else {
		SetLogLevel(slog.LevelInfo)
	}

	// Log config details without sensitive info
	mqttLogger.Info("MQTT configuration loaded",
		"broker", config.Broker,
		"client_id", config.ClientID,
		"username", config.Username, // Log username, usually not sensitive
		"topic", config.Topic,
		"retain", config.Retain,
		"debug", config.Debug,
		"tls_enabled", config.TLS.Enabled,
		"tls_skip_verify", config.TLS.InsecureSkipVerify,
	)

	return &client{
		config:        config,
		reconnectStop: make(chan struct{}),
		metrics:       observabilityMetrics.MQTT,
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

// IsDebug returns the current debug setting in a thread-safe manner.
func (c *client) IsDebug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config.Debug
}

// SetDebug updates the debug setting in a thread-safe manner.
// NOTE: This also changes the global MQTT logger level for the entire service.
func (c *client) SetDebug(debug bool) {
	c.mu.Lock()
	// Check if the value is actually changing to avoid unnecessary work
	isChanging := c.config.Debug != debug
	c.config.Debug = debug
	c.mu.Unlock() // Unlock before calling SetLogLevel

	if isChanging {
		if debug {
			mqttLogger.Debug("Client debug mode enabled, setting global MQTT log level to DEBUG")
			SetLogLevel(slog.LevelDebug)
		} else {
			mqttLogger.Debug("Client debug mode disabled, setting global MQTT log level to INFO") // Log at debug level *before* changing it
			SetLogLevel(slog.LevelInfo)
		}
	}
}

// Connect attempts to establish a connection to the MQTT broker.
// It holds the mutex only while checking state and creating the client instance,
// releasing it before blocking network operations.
func (c *client) Connect(ctx context.Context) error {
	return c.connectWithOptions(ctx, false)
}

// connectWithOptions is the internal connect method that supports bypassing cooldown for automatic reconnects.
//
// The cooldown mechanism serves different purposes for manual vs automatic connections:
//   - Manual connections (isAutoReconnect=false): Protected by ReconnectCooldown (typically 5s)
//     to prevent users or external systems from spamming connection attempts
//   - Automatic reconnects (isAutoReconnect=true): Controlled by ReconnectDelay (typically 1s)
//     which is applied by the reconnection timer before calling this method
//
// This separation is critical because:
// 1. Automatic reconnects are already rate-limited by ReconnectDelay timer
// 2. Applying ReconnectCooldown (5s) would block legitimate auto-reconnects scheduled at ReconnectDelay (1s)
// 3. Manual connection attempts still need protection against rapid retry attempts
//
// Timeline example of the conflict this solves:
// - T+0s: Connection lost, onConnectionLost called
// - T+1s: ReconnectDelay expires, automatic reconnect attempted
// - Without bypass: Blocked by 5s cooldown → "connection attempt too recent" error
// - With bypass: Reconnect proceeds as intended
func (c *client) connectWithOptions(ctx context.Context, isAutoReconnect bool) error {
	if err := ctx.Err(); err != nil { // Check context early
		mqttLogger.Warn("Connect context already cancelled", "error", err)
		return err
	}

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	c.logConnectionAttempt(logger, isAutoReconnect)

	// Phase 1: Prepare for connection (handles cooldown and old client)
	if err := c.prepareForConnection(logger, isAutoReconnect); err != nil {
		return err
	}

	// Phase 2: Create new client under lock
	clientToConnect, err := c.createNewClient(logger)
	if err != nil {
		return err
	}

	// Phase 3: Perform DNS resolution if needed
	if err := c.performDNSResolution(ctx, logger); err != nil {
		return err
	}

	// Phase 4: Attempt connection with timeout handling
	connectErr := c.performConnectionAttempt(ctx, clientToConnect, logger)

	c.mu.Lock()
	c.lastConnAttempt = time.Now()
	c.mu.Unlock()

	if connectErr != nil {
		return c.handleConnectionFailure(connectErr, clientToConnect, logger)
	}

	logger.Info("Successfully connected to MQTT broker")
	return nil
}

// logConnectionAttempt logs the appropriate message based on connection type
func (c *client) logConnectionAttempt(logger *slog.Logger, isAutoReconnect bool) {
	if isAutoReconnect {
		logger.Info("Attempting automatic reconnect to MQTT broker")
	} else {
		logger.Info("Attempting to connect to MQTT broker")
	}
}

// prepareForConnection handles cooldown check and disconnects old client if needed
func (c *client) prepareForConnection(logger *slog.Logger, isAutoReconnect bool) error {
	c.mu.Lock()
	// Only check cooldown for manual connection attempts, not automatic reconnects
	if !isAutoReconnect {
		if err := c.checkConnectionCooldownLocked(logger); err != nil {
			c.mu.Unlock()
			return err
		}
	}

	// Disconnect existing client if needed
	var oldClientToDisconnect mqtt.Client
	if c.internalClient != nil && c.internalClient.IsConnected() {
		logger.Info("Marking existing client for disconnection before reconnecting")
		oldClientToDisconnect = c.internalClient
	}
	c.mu.Unlock()

	// Perform disconnection outside the lock
	if oldClientToDisconnect != nil {
		disconnectTimeoutMs := durationToMillisUint(GracefulDisconnectTimeout)
		logger.Debug("Disconnecting old client instance", "timeout_ms", disconnectTimeoutMs)
		oldClientToDisconnect.Disconnect(disconnectTimeoutMs)
	}
	return nil
}

// createNewClient creates and configures a new MQTT client instance
func (c *client) createNewClient(logger *slog.Logger) (mqtt.Client, error) {
	// Create and configure client options outside the lock to avoid holding it during
	// potential file I/O (TLS certificate loading). configureClientOptions only reads
	// from c.config which is not modified concurrently.
	opts, err := c.configureClientOptions(logger)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Reinitialize reconnectStop if it was closed by a previous Disconnect()
	c.reinitializeReconnectStopLocked(logger)

	// Create and store the new client instance
	c.internalClient = mqtt.NewClient(opts)
	logger.Debug("MQTT client options configured and new client created",
		"keepalive", KeepAliveInterval,
		"ping_timeout", PingTimeout,
		"write_timeout", WriteTimeout,
		"connect_timeout", c.config.ConnectTimeout,
		"clean_session", true,
	)
	return c.internalClient, nil
}

// reinitializeReconnectStopLocked resets the reconnect stop channel if closed
// CALLER MUST HOLD c.mu LOCK
func (c *client) reinitializeReconnectStopLocked(logger *slog.Logger) {
	select {
	case <-c.reconnectStop:
		c.reconnectStop = make(chan struct{})
		logger.Debug("Reinitialized reconnectStop channel for new connection session")
	default:
		// Channel is still open, leave it intact
	}
}

// handleConnectionFailure processes connection errors and updates metrics
func (c *client) handleConnectionFailure(connectErr error, clientToConnect mqtt.Client, logger *slog.Logger) error {
	logger.Error("MQTT connection failed", "error", connectErr)

	// Ensure metrics reflect failure
	c.mu.Lock()
	if c.internalClient == clientToConnect && !c.internalClient.IsConnected() {
		c.metrics.UpdateConnectionStatus(false)
	}
	c.mu.Unlock()

	// Enhance error if needed
	var enhancedErr *errors.EnhancedError
	if !errors.As(connectErr, &enhancedErr) {
		connectErr = errors.New(connectErr).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "mqtt_connect").
			Context("connect_timeout", c.config.ConnectTimeout).
			Build()
	}
	return connectErr
}

// Publish sends a message to the specified topic on the MQTT broker.
func (c *client) Publish(ctx context.Context, topic, payload string) error {
	// Check context before acquiring lock
	if err := ctx.Err(); err != nil {
		mqttLogger.Warn("Publish context already cancelled", "topic", topic, "error", err)
		return err
	}

	c.mu.Lock() // Lock to safely read internalClient and check connection status
	// Directly check the internal client state while holding the lock
	// Avoids calling IsConnected() which would re-lock.
	if c.internalClient == nil || !c.internalClient.IsConnected() {
		c.mu.Unlock() // Unlock before returning error
		mqttLogger.Warn("Publish failed: client is not connected")
		enhancedErr := errors.Newf("not connected to MQTT broker").
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("topic", topic).
			Context("operation", "publish_not_connected").
			Build()
		return enhancedErr
	}
	mqttLogger.Debug("Client is connected, continuing")
	clientToPublish := c.internalClient // Get client instance under lock
	currentRetain := c.config.Retain    // Get config value under lock
	c.mu.Unlock()                       // Unlock before blocking publish call

	logger := mqttLogger.With("topic", topic, "qos", defaultQoS, "retain", currentRetain)
	timer := c.metrics.StartPublishTimer()
	defer timer.ObserveDuration()

	logger.Debug("Attempting to publish message", "payload_size", len(payload))

	// Perform the publish operation directly
	token := clientToPublish.Publish(topic, defaultQoS, currentRetain, payload)

	// Wait directly on the token with timeout
	if !token.WaitTimeout(c.config.PublishTimeout) {
		logger.Error("MQTT publish timed out", "timeout", c.config.PublishTimeout)
		// Check if the *original* context was cancelled
		if ctxErr := ctx.Err(); ctxErr != nil {
			logger.Error("Context was cancelled during publish wait", "error", ctxErr)
			return ctxErr
		}
		// If context is okay, return a specific timeout error
		c.metrics.IncrementErrorsWithCategory("mqtt-publish", "publish_timeout") // Count timeout as an error
		enhancedErr := errors.Newf("publish timeout after %v", c.config.PublishTimeout).
			Component("mqtt").
			Category(errors.CategoryMQTTPublish).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("topic", topic).
			Context("publish_timeout", c.config.PublishTimeout).
			Context("payload_size", len(payload)).
			Context("operation", "publish_timeout").
			Build()
		return enhancedErr
	}

	// Check token for errors after waiting
	if publishErr := token.Error(); publishErr != nil {
		logger.Error("MQTT publish failed", "error", publishErr)
		c.metrics.IncrementErrorsWithCategory("mqtt-publish", "publish_error")
		enhancedErr := errors.New(publishErr).
			Component("mqtt").
			Category(errors.CategoryMQTTPublish).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("topic", topic).
			Context("payload_size", len(payload)).
			Context("qos", defaultQoS).
			Context("retain", currentRetain).
			Context("operation", "publish_error").
			Build()
		return enhancedErr
	}

	// Only increment success metrics if the publish call did not return an error
	logger.Debug("Publish completed successfully")
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
	// Reduce log noise by removing debug log from here
	// mqttLogger.Debug("Checking MQTT connection status", "is_connected", connected)
	return connected
}

// calculateCancelTimeout computes a safe timeout for canceling connection attempts
func (c *client) calculateCancelTimeout() uint {
	ms := c.config.DisconnectTimeout.Milliseconds()
	defaultTimeout := durationToMillisUint(CancelDisconnectTimeout)
	if ms <= 0 {
		return defaultTimeout
	}
	candidate := max(1, durationToMillisUint(c.config.DisconnectTimeout)/5)
	cancelTimeout := min(defaultTimeout, candidate)
	// Final guard against zero
	if cancelTimeout == 0 {
		return defaultTimeout
	}
	return cancelTimeout
}

// cancelConnectionAttempt disconnects the client to prevent goroutine leaks
func (c *client) cancelConnectionAttempt(clientToConnect mqtt.Client, logger *slog.Logger) {
	if clientToConnect == nil {
		return
	}
	cancelTimeout := c.calculateCancelTimeout()
	logger.Debug("Calling Disconnect with dynamic timeout to cancel connection attempt and prevent goroutine leak",
		"timeout_ms", cancelTimeout,
		"base_timeout", durationToMillisUint(CancelDisconnectTimeout),
		"config_timeout_ms", c.config.DisconnectTimeout.Milliseconds())
	clientToConnect.Disconnect(cancelTimeout)
}

// checkConnectionCooldownLocked validates if enough time has passed since the last connection attempt
// CALLER MUST HOLD c.mu LOCK (either read or write lock)
func (c *client) checkConnectionCooldownLocked(logger *slog.Logger) error {
	// Read shared state - caller must hold lock to prevent races
	lastConnAttempt := c.lastConnAttempt
	reconnectCooldown := c.config.ReconnectCooldown
	broker := c.config.Broker
	clientID := c.config.ClientID

	if time.Since(lastConnAttempt) < reconnectCooldown {
		lastAttemptAgo := time.Since(lastConnAttempt)
		// Round to seconds for better readability
		lastAttemptRounded := lastAttemptAgo.Round(time.Second)
		// Handle sub-second durations that round to 0
		if lastAttemptRounded == 0 && lastAttemptAgo > 0 {
			lastAttemptRounded = time.Second // Display as "1s ago" instead of "0s ago"
		}
		logger.Warn("Connection attempt too recent", "last_attempt_ago", lastAttemptRounded, "cooldown", reconnectCooldown)
		return errors.Newf("connection attempt too recent, last attempt was %v ago", lastAttemptRounded).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", broker).
			Context("client_id", clientID).
			Context("last_attempt_ago", lastAttemptRounded).
			Context("cooldown", reconnectCooldown).
			Build()
	}
	return nil
}

// configureClientOptions creates and configures MQTT client options
func (c *client) configureClientOptions(logger *slog.Logger) (*mqtt.ClientOptions, error) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.config.Broker)
	opts.SetClientID(c.config.ClientID)
	opts.SetUsername(c.config.Username)
	opts.SetPassword(c.config.Password) // Do not log the password
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(false) // We'll handle reconnection ourselves
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)
	opts.SetConnectRetry(false) // We'll handle retries ourselves
	opts.SetKeepAlive(KeepAliveInterval)
	opts.SetPingTimeout(PingTimeout)
	opts.SetWriteTimeout(WriteTimeout)
	opts.SetConnectTimeout(c.config.ConnectTimeout) // Use config timeout for initial connection attempt

	// Configure TLS if enabled
	if c.config.TLS.Enabled {
		tlsConfig, err := c.createTLSConfig()
		if err != nil {
			logger.Error("Failed to create TLS configuration", "error", err)
			return nil, errors.New(err).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("operation", "create_tls_config").
				Build()
		}
		opts.SetTLSConfig(tlsConfig)
		logger.Debug("TLS configuration applied",
			"skip_verify", c.config.TLS.InsecureSkipVerify,
			"has_ca_cert", c.config.TLS.CACert != "",
			"has_client_cert", c.config.TLS.ClientCert != "",
		)
	}

	return opts, nil
}

// performDNSResolution resolves the broker hostname if it's not an IP address
func (c *client) performDNSResolution(ctx context.Context, logger *slog.Logger) error {
	// Parse the broker URL
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		logger.Error("Invalid broker URL", "error", err)
		return errors.New(err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "parse_broker_url").
			Build()
	}

	// Perform DNS resolution (potentially blocking network I/O)
	dnsCtx, dnsCancel := context.WithTimeout(ctx, DNSLookupTimeout)
	defer dnsCancel()
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		logger.Debug("Resolving broker hostname", "host", host)
		_, err := net.DefaultResolver.LookupHost(dnsCtx, host)
		if err != nil {
			// Prioritize parent context cancellation over DNS-specific errors
			if ctx.Err() != nil {
				logger.Error("Context cancelled during DNS resolution", "host", host, "error", ctx.Err())
				c.mu.Lock()
				c.lastConnAttempt = time.Now()
				c.mu.Unlock()
				return ctx.Err() // Return the original context error
			}

			// Handle DNS-specific errors
			logger.Error("Failed to resolve broker hostname", "host", host, "error", err)
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) {
				c.mu.Lock()
				c.lastConnAttempt = time.Now()
				c.mu.Unlock()
				return errors.New(dnsErr).
					Component("mqtt").
					Category(errors.CategoryNetwork).
					Context("broker", c.config.Broker).
					Context("client_id", c.config.ClientID).
					Context("hostname", host).
					Context("operation", "dns_resolution").
					Build()
			}

			// Handle other network errors
			c.mu.Lock()
			c.lastConnAttempt = time.Now()
			c.mu.Unlock()
			return errors.New(err).
				Component("mqtt").
				Category(errors.CategoryNetwork).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("hostname", host).
				Context("operation", "dns_resolution").
				Build()
		}
		logger.Debug("Broker hostname resolved successfully", "host", host)
	}
	return nil
}

// performConnectionAttempt handles the actual MQTT connection with timeout management
func (c *client) performConnectionAttempt(ctx context.Context, clientToConnect mqtt.Client, logger *slog.Logger) error {
	logger.Debug("Starting blocking connection attempt")
	token := clientToConnect.Connect()

	opDone := make(chan struct{})
	go func() {
		if !token.WaitTimeout(c.config.ConnectTimeout) {
			mqttLogger.Debug("paho.token.WaitTimeout returned false, indicating its internal timeout likely expired")
		}
		close(opDone)
	}()

	timeoutDuration := c.config.ConnectTimeout + ConnectTimeoutGrace
	timer := time.NewTimer(timeoutDuration)
	defer timer.Stop()

	select {
	case <-opDone:
		drainTimer(timer)
		return c.handleConnectionResult(token, clientToConnect, logger)
	case <-timer.C:
		logger.Error("MQTT connection attempt timed out by client.go select", "timeout", timeoutDuration)
		c.cancelConnectionAttempt(clientToConnect, logger)
		return c.buildTimeoutError(timeoutDuration)
	case <-ctx.Done():
		drainTimer(timer)
		logger.Error("Context cancelled during MQTT connection wait", "error", ctx.Err())
		c.cancelConnectionAttempt(clientToConnect, logger)
		return ctx.Err()
	}
}

// drainTimer stops timer and drains channel to prevent goroutine leaks
func drainTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// handleConnectionResult processes the result of a completed connection attempt
func (c *client) handleConnectionResult(token mqtt.Token, clientToConnect mqtt.Client, logger *slog.Logger) error {
	connectErr := token.Error()
	if connectErr != nil {
		return connectErr
	}
	if clientToConnect.IsConnected() {
		return nil
	}
	// Token succeeded but client not connected - determine appropriate error
	return c.buildNotConnectedError(logger)
}

// buildNotConnectedError creates an error for when token succeeds but client isn't connected
func (c *client) buildNotConnectedError(logger *slog.Logger) error {
	if c.config.ConnectTimeout < MinConnectTimeout {
		logger.Warn("Connection failed with very short timeout, treating as timeout scenario", "timeout", c.config.ConnectTimeout)
		return errors.Newf("mqtt connection timeout - connection failed with short timeout (%v)", c.config.ConnectTimeout).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "connect_timeout_short").
			Context("connect_timeout", c.config.ConnectTimeout).
			Build()
	}
	logger.Warn("Paho token wait completed but client not connected, no explicit token error.")
	return errors.Newf("mqtt connection failed post-wait, client not connected").
		Component("mqtt").
		Category(errors.CategoryMQTTConnection).
		Context("broker", c.config.Broker).
		Context("client_id", c.config.ClientID).
		Context("operation", "connect_post_wait").
		Context("connect_timeout", c.config.ConnectTimeout).
		Build()
}

// buildTimeoutError creates an error for connection timeout
func (c *client) buildTimeoutError(timeoutDuration time.Duration) error {
	return errors.Newf("mqtt connection attempt actively timed out by client wrapper after %v", timeoutDuration).
		Component("mqtt").
		Category(errors.CategoryMQTTConnection).
		Context("broker", c.config.Broker).
		Context("client_id", c.config.ClientID).
		Context("operation", "connect_timeout").
		Context("timeout_duration", timeoutDuration).
		Build()
}

// Disconnect closes the connection to the MQTT broker.
// Uses ShutdownDisconnectTimeout if configured (non-zero), otherwise falls back to DisconnectTimeout.
// This allows for shorter timeouts during application shutdown.
func (c *client) Disconnect() {
	// Choose timeout: ShutdownDisconnectTimeout if set, otherwise DisconnectTimeout
	timeout := c.config.DisconnectTimeout
	if c.config.ShutdownDisconnectTimeout > 0 {
		timeout = c.config.ShutdownDisconnectTimeout
	}

	// Normalize timeout to prevent zero or negative values that could cause underflow
	// when converted to unsigned types
	if timeout <= 0 {
		// Fall back to a safe default if both timeouts are non-positive
		timeout = GracefulDisconnectTimeout
	}

	c.disconnectWithTimeout(timeout)
}

// disconnectWithTimeout closes the connection with a specific timeout
func (c *client) disconnectWithTimeout(timeout time.Duration) {
	c.mu.Lock() // Lock required to safely access reconnectStop, reconnectTimer, internalClient

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
		c.reconnectTimer = nil // Prevent future use after stopping
	}

	clientToDisconnect := c.internalClient // Get client instance under lock
	c.internalClient = nil                 // Clear internal client reference under lock
	c.mu.Unlock()                          // Unlock before potentially blocking disconnect

	if clientToDisconnect != nil {
		// Check connection status *outside* lock to avoid potential deadlock
		// if IsConnected internally needs a lock (though it uses RLock)
		if clientToDisconnect.IsConnected() {
			disconnectTimeoutMs := uint(timeout.Milliseconds()) // #nosec G115 -- timeout value conversion safe
			logger.Debug("Sending disconnect signal to Paho client", "timeout_ms", disconnectTimeoutMs)
			clientToDisconnect.Disconnect(disconnectTimeoutMs) // Perform disconnect outside lock
			c.metrics.UpdateConnectionStatus(false)            // Update metrics after disconnect attempt
		} else {
			logger.Debug("Client was not connected when disconnect called")
			// Ensure status is marked as false if we clear a non-nil but disconnected client
			c.metrics.UpdateConnectionStatus(false)
		}
	} else {
		logger.Debug("Client was not initialized when disconnect called")
		// Ensure status is marked as false if we are disconnecting with no client
		c.metrics.UpdateConnectionStatus(false)
	}
}

func (c *client) onConnect(client mqtt.Client) {
	// Log using the package-level logger
	mqttLogger.Info("Connected to MQTT broker", "broker", c.config.Broker, "client_id", c.config.ClientID)
	c.metrics.UpdateConnectionStatus(true)
	// Reset reconnect attempts on successful connection - might be handled by Connect logic resetting lastConnAttempt implicitly
}

func (c *client) onConnectionLost(client mqtt.Client, err error) {
	// Enhance the connection lost error
	enhancedErr := errors.New(err).
		Component("mqtt").
		Category(errors.CategoryMQTTConnection).
		Context("broker", c.config.Broker).
		Context("client_id", c.config.ClientID).
		Context("operation", "connection_lost").
		Build()

	// Log using the package-level logger
	mqttLogger.Error("Connection to MQTT broker lost", "broker", c.config.Broker, "client_id", c.config.ClientID, "error", enhancedErr)
	c.metrics.UpdateConnectionStatus(false)
	c.metrics.IncrementErrorsWithCategory("mqtt-connection", "connection_lost")

	// Send notification for connection lost
	notification.NotifyIntegrationFailure("MQTT", enhancedErr)
	// Check if we should attempt to reconnect or if Disconnect was called
	select {
	case <-c.reconnectStop:
		mqttLogger.Info("Reconnect mechanism stopped, not attempting reconnect.")
		return
	default:
		// Proceed with reconnect
		c.startReconnectTimer()
	}
}

func (c *client) startReconnectTimer() {
	c.mu.Lock() // Lock to safely modify reconnectTimer
	defer c.mu.Unlock()

	// Ensure we don't start multiple timers if called rapidly
	if c.reconnectTimer != nil {
		mqttLogger.Debug("Reconnect timer already active, stopping previous one.")
		c.reconnectTimer.Stop()
	}

	reconnectDelay := c.config.ReconnectDelay
	mqttLogger.Info("Starting reconnect timer", "delay", reconnectDelay)
	c.reconnectTimer = time.AfterFunc(reconnectDelay, func() {
		select {
		case <-c.reconnectStop: // Check if disconnect was called before timer fired
			mqttLogger.Info("Reconnect cancelled before execution")
			return
		default:
			// Run reconnect logic in a separate goroutine to avoid blocking timer goroutine
			go c.reconnectWithBackoff()
		}
	})
}

func (c *client) reconnectWithBackoff() {
	// Use a context with a timeout longer than the connect timeout itself,
	// to allow for DNS lookup etc. Add a buffer.
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout+ReconnectContextGrace)
	defer cancel()

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	logger.Info("Attempting to reconnect to MQTT broker")

	// Check if reconnect process was stopped before attempting connection
	select {
	case <-c.reconnectStop:
		logger.Info("Reconnect mechanism stopped during backoff, aborting reconnect attempt.")
		return
	default:
		// Proceed with connect attempt
	}

	// Use connectWithOptions with isAutoReconnect=true to bypass cooldown check
	if err := c.connectWithOptions(ctx, true); err != nil {
		logger.Error("Reconnect attempt failed", "error", err)

		// Extract error category for metrics
		errorCategory := "generic"
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) {
			errorCategory = enhancedErr.GetCategory()
		}
		c.metrics.IncrementErrorsWithCategory(errorCategory, "reconnect_failed")

		// Check if stopped *after* failed attempt before rescheduling
		select {
		case <-c.reconnectStop:
			logger.Info("Reconnect mechanism stopped after failed attempt, not rescheduling.")
			return
		default:
			// Schedule next attempt
			c.startReconnectTimer() // Reschedule another attempt after delay
		}
	} else {
		// Connection successful, logged by onConnect
		logger.Info("Reconnect successful")
		// No need to call startReconnectTimer here, connection is established
	}
}

// createTLSConfig creates a TLS configuration based on the client settings
func (c *client) createTLSConfig() (*tls.Config, error) {
	hostname, err := c.extractBrokerHostname()
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		ServerName: hostname,
		MinVersion: tls.VersionTLS12,
		// WARNING: InsecureSkipVerify disables certificate verification.
		// This makes the connection vulnerable to man-in-the-middle attacks.
		// Only use for testing or with self-signed certificates in trusted networks.
		InsecureSkipVerify: c.config.TLS.InsecureSkipVerify, // #nosec G402 -- InsecureSkipVerify is controlled by user configuration for self-signed certificates
	}

	// Load CA certificate if provided
	if c.config.TLS.CACert != "" {
		if err := c.loadCACertificate(tlsConfig); err != nil {
			return nil, err
		}
	}

	// Load client certificate and key if provided
	if c.config.TLS.ClientCert != "" && c.config.TLS.ClientKey != "" {
		if err := c.loadClientCertificate(tlsConfig); err != nil {
			return nil, err
		}
	}

	return tlsConfig, nil
}

// extractBrokerHostname extracts the hostname from the broker URL
func (c *client) extractBrokerHostname() (string, error) {
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		return "", errors.Newf("failed to parse broker URL for TLS config: %v", err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("broker", c.config.Broker).
			Build()
	}
	return u.Hostname(), nil
}

// loadCACertificate loads the CA certificate into the TLS config
func (c *client) loadCACertificate(tlsConfig *tls.Config) error {
	if _, err := os.Stat(c.config.TLS.CACert); os.IsNotExist(err) {
		return errors.Newf("CA certificate file does not exist: %s", c.config.TLS.CACert).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("ca_cert_path", c.config.TLS.CACert).
			Build()
	}

	caCert, err := os.ReadFile(c.config.TLS.CACert)
	if err != nil {
		return errors.Newf("failed to read CA certificate file: %v", err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("ca_cert_path", c.config.TLS.CACert).
			Build()
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return errors.Newf("failed to parse CA certificate").
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("ca_cert_path", c.config.TLS.CACert).
			Build()
	}
	tlsConfig.RootCAs = caCertPool
	mqttLogger.Debug("CA certificate loaded", "path", c.config.TLS.CACert)
	return nil
}

// loadClientCertificate loads the client certificate and key into the TLS config
func (c *client) loadClientCertificate(tlsConfig *tls.Config) error {
	if _, err := os.Stat(c.config.TLS.ClientCert); os.IsNotExist(err) {
		return errors.Newf("client certificate file does not exist: %s", c.config.TLS.ClientCert).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("client_cert_path", c.config.TLS.ClientCert).
			Build()
	}

	if _, err := os.Stat(c.config.TLS.ClientKey); os.IsNotExist(err) {
		return errors.Newf("client key file does not exist: %s", c.config.TLS.ClientKey).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("client_key_path", c.config.TLS.ClientKey).
			Build()
	}

	cert, err := tls.LoadX509KeyPair(c.config.TLS.ClientCert, c.config.TLS.ClientKey)
	if err != nil {
		return errors.Newf("failed to load client certificate and key: %v", err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("client_cert_path", c.config.TLS.ClientCert).
			Context("client_key_path", c.config.TLS.ClientKey).
			Build()
	}
	tlsConfig.Certificates = []tls.Certificate{cert}
	mqttLogger.Debug("Client certificate loaded",
		"cert_path", c.config.TLS.ClientCert,
		"key_path", c.config.TLS.ClientKey,
	)
	return nil
}
