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
	if err := ctx.Err(); err != nil { // Check context early
		mqttLogger.Warn("Connect context already cancelled", "error", err)
		return err
	}

	logger := mqttLogger.With("broker", c.config.Broker, "client_id", c.config.ClientID)
	logger.Info("Attempting to connect to MQTT broker")

	// --- Lock acquisition START ---
	c.mu.Lock()
	if time.Since(c.lastConnAttempt) < c.config.ReconnectCooldown {
		lastAttemptAgo := time.Since(c.lastConnAttempt)
		c.mu.Unlock() // Unlock before returning
		logger.Warn("Connection attempt too recent", "last_attempt_ago", lastAttemptAgo, "cooldown", c.config.ReconnectCooldown)
		enhancedErr := errors.Newf("connection attempt too recent, last attempt was %v ago", lastAttemptAgo).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("last_attempt_ago", lastAttemptAgo).
			Context("cooldown", c.config.ReconnectCooldown).
			Build()
		return enhancedErr
	}

	// Disconnect existing client if needed - requires lock
	var oldClientToDisconnect mqtt.Client
	if c.internalClient != nil && c.internalClient.IsConnected() {
		logger.Info("Marking existing client for disconnection before reconnecting")
		oldClientToDisconnect = c.internalClient // Copy pointer under lock
	}
	c.mu.Unlock() // Release lock BEFORE potentially blocking disconnect call

	// Perform disconnection outside the lock
	if oldClientToDisconnect != nil {
		logger.Debug("Disconnecting old client instance", "timeout_ms", 5000)
		oldClientToDisconnect.Disconnect(5000) // Use longer timeout for graceful disconnect
	}

	// --- Re-acquire lock to modify shared state ---
	c.mu.Lock() // Re-acquire lock for client options and creation

	// Create connection options - can be outside lock, but simpler here
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
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(10 * time.Second)
	opts.SetWriteTimeout(10 * time.Second)
	opts.SetConnectTimeout(c.config.ConnectTimeout) // Use config timeout for initial connection attempt

	// Configure TLS if enabled
	if c.config.TLS.Enabled {
		tlsConfig, err := c.createTLSConfig()
		if err != nil {
			c.mu.Unlock()
			logger.Error("Failed to create TLS configuration", "error", err)
			enhancedErr := errors.New(err).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("operation", "create_tls_config").
				Build()
			return enhancedErr
		}
		opts.SetTLSConfig(tlsConfig)
		logger.Debug("TLS configuration applied",
			"skip_verify", c.config.TLS.InsecureSkipVerify,
			"has_ca_cert", c.config.TLS.CACert != "",
			"has_client_cert", c.config.TLS.ClientCert != "",
		)
	}

	// Create and store the new client instance under lock
	c.internalClient = mqtt.NewClient(opts)
	clientToConnect := c.internalClient // Local variable to use after unlock

	logger.Debug("MQTT client options configured and new client created",
		"keepalive", 30*time.Second,
		"ping_timeout", 10*time.Second,
		"write_timeout", 10*time.Second,
		"connect_timeout", c.config.ConnectTimeout,
		"clean_session", true,
	)
	c.mu.Unlock()
	// --- Lock acquisition END ---

	// --- Operations outside the lock ---

	// Parse the broker URL (no shared state access needed)
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		logger.Error("Invalid broker URL", "error", err)
		enhancedErr := errors.New(err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "parse_broker_url").
			Build()
		return enhancedErr
	}

	// Perform DNS resolution (potentially blocking network I/O)
	dnsCtx, dnsCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dnsCancel()
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		logger.Debug("Resolving broker hostname", "host", host)
		_, err := net.DefaultResolver.LookupHost(dnsCtx, host)
		if err != nil {
			// Check if context expired during DNS lookup
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == context.DeadlineExceeded {
				logger.Error("Context deadline exceeded during DNS resolution", "host", host, "error", ctx.Err())
				c.mu.Lock()
				c.lastConnAttempt = time.Now()
				c.mu.Unlock()
				return ctx.Err() // Return the original context error
			}
			logger.Error("Failed to resolve broker hostname", "host", host, "error", err)
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) {
				c.mu.Lock()
				c.lastConnAttempt = time.Now()
				c.mu.Unlock()
				enhancedErr := errors.New(dnsErr).
					Component("mqtt").
					Category(errors.CategoryNetwork).
					Context("broker", c.config.Broker).
					Context("client_id", c.config.ClientID).
					Context("hostname", host).
					Context("operation", "dns_resolution").
					Build()
				return enhancedErr
			}
			c.mu.Lock()
			c.lastConnAttempt = time.Now()
			c.mu.Unlock()
			enhancedErr := errors.New(err).
				Component("mqtt").
				Category(errors.CategoryNetwork).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("hostname", host).
				Context("operation", "dns_resolution").
				Build()
			return enhancedErr
		}
		logger.Debug("Broker hostname resolved successfully", "host", host)
	}

	// --- Actual connection attempt (blocking) ---
	logger.Debug("Starting blocking connection attempt")
	token := clientToConnect.Connect() // Use the local variable

	var connectErr error
	opDone := make(chan struct{})

	go func() {
		// token.Wait() can block indefinitely if ConnectTimeout in Paho is not effective for all cases.
		// We are primarily relying on the select below for timeout.
		// However, WaitTimeout is still useful if Paho's timeout *is* shorter for some reason.
		// Using WaitTimeout here also allows Paho to set its internal error state correctly on its timeout.
		if !token.WaitTimeout(c.config.ConnectTimeout) {
			// This branch is taken if Paho's ConnectTimeout expires.
			// The select below will likely also hit its own timeout case almost simultaneously or shortly after.
			// Error from token.Error() should reflect the Paho timeout.
			mqttLogger.Debug("paho.token.WaitTimeout returned false, indicating its internal timeout likely expired; actual error/timeout is handled by the main select block")
		}
		close(opDone)
	}()

	select {
	case <-opDone:
		connectErr = token.Error()
		if connectErr == nil && !clientToConnect.IsConnected() {
			// If token.Error() is nil but not connected, it implies Paho's WaitTimeout might have returned true
			// because the "wait" finished, but the connection itself failed without an error on the token.
			// This can happen if the timeout was very short.
			connectErr = errors.Newf("mqtt connection failed post-wait, client not connected").
				Component("mqtt").
				Category(errors.CategoryMQTTConnection).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("operation", "connect_post_wait").
				Context("connect_timeout", c.config.ConnectTimeout).
				Build()
			logger.Warn("Paho token wait completed but client not connected, no explicit token error.")
		}
	case <-time.After(c.config.ConnectTimeout + 500*time.Millisecond): // Add a small grace period over Paho's configured timeout
		timeoutDuration := c.config.ConnectTimeout + 500*time.Millisecond
		connectErr = errors.Newf("mqtt connection attempt actively timed out by client wrapper after %v", timeoutDuration).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "connect_timeout").
			Context("timeout_duration", timeoutDuration).
			Build()
		logger.Error("MQTT connection attempt timed out by client.go select", "timeout", timeoutDuration)
		// Cancel the connection attempt to prevent the goroutine from leaking
		if clientToConnect != nil {
			logger.Debug("Calling Disconnect(1000) to cancel connection attempt and prevent goroutine leak")
			clientToConnect.Disconnect(1000) // Use timeout for graceful disconnection
		}
	case <-ctx.Done():
		connectErr = ctx.Err()
		logger.Error("Context cancelled during MQTT connection wait", "error", connectErr)
		// Cancel the connection attempt to prevent the goroutine from leaking
		if clientToConnect != nil {
			logger.Debug("Calling Disconnect(1000) to cancel connection attempt and prevent goroutine leak")
			clientToConnect.Disconnect(1000) // Use timeout for graceful disconnection
		}
	}

	c.mu.Lock()
	c.lastConnAttempt = time.Now()
	c.mu.Unlock()

	if connectErr != nil {
		logger.Error("MQTT connection failed", "error", connectErr)
		// Ensure metrics reflect failure if onConnect wasn't called.
		c.mu.Lock()
		// Check if c.internalClient is still the one we attempted to connect.
		// It might have been changed by a concurrent Disconnect/Connect sequence, though unlikely with current locking.
		if c.internalClient == clientToConnect && !c.internalClient.IsConnected() {
			c.metrics.UpdateConnectionStatus(false)
		}
		c.mu.Unlock()

		// If connectErr is not already an enhanced error, enhance it
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

	// If we reach here, connectErr was nil from the select block
	logger.Info("Successfully connected to MQTT broker")
	// onConnect handler should be called by Paho, which updates metrics.
	// If onConnect was somehow not called despite a successful connection,
	// metrics might be out of sync. This state is unlikely with Paho.
	return nil
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

// Disconnect closes the connection to the MQTT broker.
func (c *client) Disconnect() {
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
			disconnectTimeoutMs := uint(c.config.DisconnectTimeout.Milliseconds()) // #nosec G115 -- timeout value conversion safe
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
	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectTimeout+10*time.Second)
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

	if err := c.Connect(ctx); err != nil {
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
	// Extract hostname from broker URL for ServerName
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		return nil, errors.Newf("failed to parse broker URL for TLS config: %v", err).
			Component("mqtt").
			Category(errors.CategoryConfiguration).
			Context("broker", c.config.Broker).
			Build()
	}
	
	hostname := u.Hostname()
	
	tlsConfig := &tls.Config{
		ServerName:         hostname,
		MinVersion:         tls.VersionTLS12,
		// WARNING: InsecureSkipVerify disables certificate verification.
		// This makes the connection vulnerable to man-in-the-middle attacks.
		// Only use for testing or with self-signed certificates in trusted networks.
		InsecureSkipVerify: c.config.TLS.InsecureSkipVerify, // #nosec G402 -- InsecureSkipVerify is controlled by user configuration for self-signed certificates
	}

	// Load CA certificate if provided
	if c.config.TLS.CACert != "" {
		// Check if file exists for better error message
		if _, err := os.Stat(c.config.TLS.CACert); os.IsNotExist(err) {
			return nil, errors.Newf("CA certificate file does not exist: %s", c.config.TLS.CACert).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("ca_cert_path", c.config.TLS.CACert).
				Build()
		}
		
		caCert, err := os.ReadFile(c.config.TLS.CACert)
		if err != nil {
			return nil, errors.Newf("failed to read CA certificate file: %v", err).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("ca_cert_path", c.config.TLS.CACert).
				Build()
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, errors.Newf("failed to parse CA certificate").
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("ca_cert_path", c.config.TLS.CACert).
				Build()
		}
		tlsConfig.RootCAs = caCertPool
		mqttLogger.Debug("CA certificate loaded", "path", c.config.TLS.CACert)
	}

	// Load client certificate and key if provided
	if c.config.TLS.ClientCert != "" && c.config.TLS.ClientKey != "" {
		// Check if client certificate exists
		if _, err := os.Stat(c.config.TLS.ClientCert); os.IsNotExist(err) {
			return nil, errors.Newf("client certificate file does not exist: %s", c.config.TLS.ClientCert).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("client_cert_path", c.config.TLS.ClientCert).
				Build()
		}
		
		// Check if client key exists
		if _, err := os.Stat(c.config.TLS.ClientKey); os.IsNotExist(err) {
			return nil, errors.Newf("client key file does not exist: %s", c.config.TLS.ClientKey).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("client_key_path", c.config.TLS.ClientKey).
				Build()
		}
		
		cert, err := tls.LoadX509KeyPair(c.config.TLS.ClientCert, c.config.TLS.ClientKey)
		if err != nil {
			return nil, errors.Newf("failed to load client certificate and key: %v", err).
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
	}

	return tlsConfig, nil
}

// Helper function to get the current internal client safely
// (Not strictly needed with current refactor but could be useful)
// func (c *client) getInternalClient() mqtt.Client {
// 	c.mu.RLock()
// 	defer c.mu.RUnlock()
// 	return c.internalClient
// }
