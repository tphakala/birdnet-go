// client.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	config            Config
	internalClient    mqtt.Client
	lastConnAttempt   time.Time
	mu                sync.RWMutex
	reconnectTimer    *time.Timer
	reconnectStop     chan struct{}
	metrics           *metrics.MQTTMetrics
	controlChan       chan string        // Channel for control signals
	onConnectHandlers []OnConnectHandler // Handlers called on successful connection
}

// NewClient creates a new MQTT client with the provided configuration.
func NewClient(settings *conf.Settings, observabilityMetrics *observability.Metrics) (Client, error) {
	log := GetLogger()
	log.Info("Creating new MQTT client")
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
		log.Info("TLS enabled based on broker URL scheme")
	}

	// Configure LWT (Last Will and Testament) for Home Assistant availability tracking
	if settings.Realtime.MQTT.HomeAssistant.Enabled {
		config.LWT.Enabled = true
		config.LWT.Topic = config.Topic + "/status"
		config.LWT.Payload = "offline"
		config.LWT.QoS = 1
		config.LWT.Retain = true
		log.Info("Home Assistant auto-discovery enabled, LWT configured",
			logger.String("lwt_topic", config.LWT.Topic))
	}

	// Note: Debug mode logging is now controlled by the central logger configuration
	if config.Debug {
		log.Debug("MQTT Debug logging enabled")
	}

	// Log config details without sensitive info
	log.Info("MQTT configuration loaded",
		logger.String("broker", config.Broker),
		logger.String("client_id", config.ClientID),
		logger.Username(config.Username),
		logger.String("topic", config.Topic),
		logger.Bool("retain", config.Retain),
		logger.Bool("debug", config.Debug),
		logger.Bool("tls_enabled", config.TLS.Enabled),
		logger.Bool("tls_skip_verify", config.TLS.InsecureSkipVerify),
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
	GetLogger().Debug("Setting control channel for MQTT client")
	c.controlChan = ch
}

// RegisterOnConnectHandler registers a callback that will be invoked each time
// the client successfully connects or reconnects to the broker.
func (c *client) RegisterOnConnectHandler(handler OnConnectHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnectHandlers = append(c.onConnectHandlers, handler)
	GetLogger().Debug("Registered OnConnect handler", logger.Int("total_handlers", len(c.onConnectHandlers)))
}

// IsDebug returns the current debug setting in a thread-safe manner.
func (c *client) IsDebug() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config.Debug
}

// SetDebug updates the debug setting in a thread-safe manner.
// NOTE: Debug mode logging is now controlled by the central logger configuration.
func (c *client) SetDebug(debug bool) {
	c.mu.Lock()
	// Check if the value is actually changing to avoid unnecessary work
	isChanging := c.config.Debug != debug
	c.config.Debug = debug
	c.mu.Unlock()

	if isChanging {
		log := GetLogger()
		if debug {
			log.Debug("Client debug mode enabled")
		} else {
			log.Debug("Client debug mode disabled")
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
		GetLogger().Warn("Connect context already cancelled", logger.Error(err))
		return err
	}

	log := GetLogger().With(
		logger.String("broker", c.config.Broker),
		logger.String("client_id", c.config.ClientID),
	)
	c.logConnectionAttempt(log, isAutoReconnect)

	// Phase 1: Prepare for connection (handles cooldown and old client)
	if err := c.prepareForConnection(log, isAutoReconnect); err != nil {
		return err
	}

	// Phase 2: Create new client under lock
	clientToConnect, err := c.createNewClient(log)
	if err != nil {
		return err
	}

	// Phase 3: Perform DNS resolution if needed
	if err := c.performDNSResolution(ctx, log); err != nil {
		return err
	}

	// Phase 4: Attempt connection with timeout handling
	connectErr := c.performConnectionAttempt(ctx, clientToConnect, log)

	c.mu.Lock()
	c.lastConnAttempt = time.Now()
	c.mu.Unlock()

	if connectErr != nil {
		return c.handleConnectionFailure(connectErr, clientToConnect, log)
	}

	log.Info("Successfully connected to MQTT broker")
	return nil
}

// logConnectionAttempt logs the appropriate message based on connection type
func (c *client) logConnectionAttempt(log logger.Logger, isAutoReconnect bool) {
	if isAutoReconnect {
		log.Info("Attempting automatic reconnect to MQTT broker")
	} else {
		log.Info("Attempting to connect to MQTT broker")
	}
}

// prepareForConnection handles cooldown check and disconnects old client if needed
func (c *client) prepareForConnection(log logger.Logger, isAutoReconnect bool) error {
	c.mu.Lock()
	// Only check cooldown for manual connection attempts, not automatic reconnects
	if !isAutoReconnect {
		if err := c.checkConnectionCooldownLocked(log); err != nil {
			c.mu.Unlock()
			return err
		}
	}

	// Disconnect existing client if needed
	var oldClientToDisconnect mqtt.Client
	if c.internalClient != nil && c.internalClient.IsConnected() {
		log.Info("Marking existing client for disconnection before reconnecting")
		oldClientToDisconnect = c.internalClient
	}
	c.mu.Unlock()

	// Perform disconnection outside the lock
	if oldClientToDisconnect != nil {
		disconnectTimeoutMs := durationToMillisUint(GracefulDisconnectTimeout)
		log.Debug("Disconnecting old client instance",
			logger.Uint64("timeout_ms", uint64(disconnectTimeoutMs)))
		oldClientToDisconnect.Disconnect(disconnectTimeoutMs)
	}
	return nil
}

// createNewClient creates and configures a new MQTT client instance
func (c *client) createNewClient(log logger.Logger) (mqtt.Client, error) {
	// Create and configure client options outside the lock to avoid holding it during
	// potential file I/O (TLS certificate loading). configureClientOptions only reads
	// from c.config which is not modified concurrently.
	opts, err := c.configureClientOptions(log)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Reinitialize reconnectStop if it was closed by a previous Disconnect()
	c.reinitializeReconnectStopLocked(log)

	// Create and store the new client instance
	c.internalClient = mqtt.NewClient(opts)
	log.Debug("MQTT client options configured and new client created",
		logger.Duration("keepalive", KeepAliveInterval),
		logger.Duration("ping_timeout", PingTimeout),
		logger.Duration("write_timeout", WriteTimeout),
		logger.Duration("connect_timeout", c.config.ConnectTimeout),
		logger.Bool("clean_session", true),
	)
	return c.internalClient, nil
}

// reinitializeReconnectStopLocked resets the reconnect stop channel if closed
// CALLER MUST HOLD c.mu LOCK
func (c *client) reinitializeReconnectStopLocked(log logger.Logger) {
	select {
	case <-c.reconnectStop:
		c.reconnectStop = make(chan struct{})
		log.Debug("Reinitialized reconnectStop channel for new connection session")
	default:
		// Channel is still open, leave it intact
	}
}

// handleConnectionFailure processes connection errors and updates metrics
func (c *client) handleConnectionFailure(connectErr error, clientToConnect mqtt.Client, log logger.Logger) error {
	log.Error("MQTT connection failed", logger.Error(connectErr))

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
	c.mu.RLock()
	currentRetain := c.config.Retain
	c.mu.RUnlock()
	return c.publishInternal(ctx, topic, payload, currentRetain)
}

// PublishWithRetain sends a message with explicit retain flag control.
// This is useful for discovery messages that must be retained regardless of client config.
func (c *client) PublishWithRetain(ctx context.Context, topic, payload string, retain bool) error {
	return c.publishInternal(ctx, topic, payload, retain)
}

// publishInternal is the common implementation for Publish and PublishWithRetain.
func (c *client) publishInternal(ctx context.Context, topic, payload string, retain bool) error {
	// Check context before acquiring lock
	if err := ctx.Err(); err != nil {
		GetLogger().Warn("Publish context already cancelled",
			logger.String("topic", topic),
			logger.Error(err))
		return err
	}

	c.mu.Lock()
	if c.internalClient == nil || !c.internalClient.IsConnected() {
		c.mu.Unlock()
		GetLogger().Warn("Publish failed: client is not connected")
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
	clientToPublish := c.internalClient
	c.mu.Unlock()

	log := GetLogger().With(
		logger.String("topic", topic),
		logger.Int("qos", defaultQoS),
		logger.Bool("retain", retain))
	timer := c.metrics.StartPublishTimer()
	defer timer.ObserveDuration()

	log.Debug("Attempting to publish message",
		logger.Int("payload_size", len(payload)))

	token := clientToPublish.Publish(topic, defaultQoS, retain, payload)

	if !token.WaitTimeout(c.config.PublishTimeout) {
		log.Error("MQTT publish timed out",
			logger.Duration("timeout", c.config.PublishTimeout))
		if ctxErr := ctx.Err(); ctxErr != nil {
			log.Error("Context was cancelled during publish wait",
				logger.Error(ctxErr))
			return ctxErr
		}
		c.metrics.IncrementErrorsWithCategory("mqtt-publish", "publish_timeout")
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

	if publishErr := token.Error(); publishErr != nil {
		log.Error("MQTT publish failed",
			logger.Error(publishErr))
		c.metrics.IncrementErrorsWithCategory("mqtt-publish", "publish_error")
		enhancedErr := errors.New(publishErr).
			Component("mqtt").
			Category(errors.CategoryMQTTPublish).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("topic", topic).
			Context("payload_size", len(payload)).
			Context("qos", defaultQoS).
			Context("retain", retain).
			Context("operation", "publish_error").
			Build()
		return enhancedErr
	}

	log.Debug("Publish completed successfully")
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
func (c *client) cancelConnectionAttempt(clientToConnect mqtt.Client, log logger.Logger) {
	if clientToConnect == nil {
		return
	}
	cancelTimeout := c.calculateCancelTimeout()
	log.Debug("Calling Disconnect with dynamic timeout to cancel connection attempt and prevent goroutine leak",
		logger.Uint64("timeout_ms", uint64(cancelTimeout)),
		logger.Uint64("base_timeout", uint64(durationToMillisUint(CancelDisconnectTimeout))),
		logger.Int64("config_timeout_ms", c.config.DisconnectTimeout.Milliseconds()))
	clientToConnect.Disconnect(cancelTimeout)
}

// checkConnectionCooldownLocked validates if enough time has passed since the last connection attempt
// CALLER MUST HOLD c.mu LOCK (either read or write lock)
func (c *client) checkConnectionCooldownLocked(log logger.Logger) error {
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
		log.Warn("Connection attempt too recent",
			logger.Duration("last_attempt_ago", lastAttemptRounded),
			logger.Duration("cooldown", reconnectCooldown))
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
func (c *client) configureClientOptions(log logger.Logger) (*mqtt.ClientOptions, error) {
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
			log.Error("Failed to create TLS configuration",
				logger.Error(err))
			return nil, errors.New(err).
				Component("mqtt").
				Category(errors.CategoryConfiguration).
				Context("broker", c.config.Broker).
				Context("client_id", c.config.ClientID).
				Context("operation", "create_tls_config").
				Build()
		}
		opts.SetTLSConfig(tlsConfig)
		log.Debug("TLS configuration applied",
			logger.Bool("skip_verify", c.config.TLS.InsecureSkipVerify),
			logger.Bool("has_ca_cert", c.config.TLS.CACert != ""),
			logger.Bool("has_client_cert", c.config.TLS.ClientCert != ""),
		)
	}

	// Configure Last Will and Testament (LWT) for availability tracking
	if c.config.LWT.Enabled && c.config.LWT.Topic != "" {
		opts.SetWill(c.config.LWT.Topic, c.config.LWT.Payload, c.config.LWT.QoS, c.config.LWT.Retain)
		log.Debug("LWT configuration applied",
			logger.String("topic", c.config.LWT.Topic),
			logger.String("payload", c.config.LWT.Payload),
			logger.Int("qos", int(c.config.LWT.QoS)),
			logger.Bool("retain", c.config.LWT.Retain),
		)
	}

	return opts, nil
}

// performDNSResolution resolves the broker hostname if it's not an IP address
func (c *client) performDNSResolution(ctx context.Context, log logger.Logger) error {
	// Parse the broker URL
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		log.Error("Invalid broker URL",
			logger.Error(err))
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
		log.Debug("Resolving broker hostname",
			logger.String("host", host))
		_, err := net.DefaultResolver.LookupHost(dnsCtx, host)
		if err != nil {
			// Prioritize parent context cancellation over DNS-specific errors
			if ctx.Err() != nil {
				log.Error("Context cancelled during DNS resolution",
					logger.String("host", host),
					logger.Error(ctx.Err()))
				c.mu.Lock()
				c.lastConnAttempt = time.Now()
				c.mu.Unlock()
				return ctx.Err() // Return the original context error
			}

			// Handle DNS-specific errors
			log.Error("Failed to resolve broker hostname",
				logger.String("host", host),
				logger.Error(err))
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
		log.Debug("Broker hostname resolved successfully",
			logger.String("host", host))
	}
	return nil
}

// performConnectionAttempt handles the actual MQTT connection with timeout management
func (c *client) performConnectionAttempt(ctx context.Context, clientToConnect mqtt.Client, log logger.Logger) error {
	log.Debug("Starting blocking connection attempt")
	token := clientToConnect.Connect()

	opDone := make(chan struct{})
	go func() {
		if !token.WaitTimeout(c.config.ConnectTimeout) {
			GetLogger().Debug("paho.token.WaitTimeout returned false, indicating its internal timeout likely expired")
		}
		close(opDone)
	}()

	timeoutDuration := c.config.ConnectTimeout + ConnectTimeoutGrace
	timer := time.NewTimer(timeoutDuration)
	defer timer.Stop()

	select {
	case <-opDone:
		drainTimer(timer)
		return c.handleConnectionResult(token, clientToConnect, log)
	case <-timer.C:
		log.Error("MQTT connection attempt timed out by client.go select",
			logger.Duration("timeout", timeoutDuration))
		c.cancelConnectionAttempt(clientToConnect, log)
		return c.buildTimeoutError(timeoutDuration)
	case <-ctx.Done():
		drainTimer(timer)
		log.Error("Context cancelled during MQTT connection wait",
			logger.Error(ctx.Err()))
		c.cancelConnectionAttempt(clientToConnect, log)
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
func (c *client) handleConnectionResult(token mqtt.Token, clientToConnect mqtt.Client, log logger.Logger) error {
	connectErr := token.Error()
	if connectErr != nil {
		return connectErr
	}
	if clientToConnect.IsConnected() {
		return nil
	}
	// Token succeeded but client not connected - determine appropriate error
	return c.buildNotConnectedError(log)
}

// buildNotConnectedError creates an error for when token succeeds but client isn't connected
func (c *client) buildNotConnectedError(log logger.Logger) error {
	if c.config.ConnectTimeout < MinConnectTimeout {
		log.Warn("Connection failed with very short timeout, treating as timeout scenario",
			logger.Duration("timeout", c.config.ConnectTimeout))
		return errors.Newf("mqtt connection timeout - connection failed with short timeout (%v)", c.config.ConnectTimeout).
			Component("mqtt").
			Category(errors.CategoryMQTTConnection).
			Context("broker", c.config.Broker).
			Context("client_id", c.config.ClientID).
			Context("operation", "connect_timeout_short").
			Context("connect_timeout", c.config.ConnectTimeout).
			Build()
	}
	log.Warn("Paho token wait completed but client not connected, no explicit token error.")
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

	log := GetLogger().With(
		logger.String("broker", c.config.Broker),
		logger.String("client_id", c.config.ClientID))
	log.Info("Disconnecting from MQTT broker")

	// Signal reconnect loop to stop
	select {
	case <-c.reconnectStop:
		// Already closed
	default:
		close(c.reconnectStop)
	}

	if c.reconnectTimer != nil {
		log.Debug("Stopping reconnect timer")
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
			log.Debug("Sending disconnect signal to Paho client",
				logger.Uint64("timeout_ms", uint64(disconnectTimeoutMs)))
			clientToDisconnect.Disconnect(disconnectTimeoutMs) // Perform disconnect outside lock
			c.metrics.UpdateConnectionStatus(false)            // Update metrics after disconnect attempt
		} else {
			log.Debug("Client was not connected when disconnect called")
			// Ensure status is marked as false if we clear a non-nil but disconnected client
			c.metrics.UpdateConnectionStatus(false)
		}
	} else {
		GetLogger().Debug("Client was not initialized when disconnect called")
		// Ensure status is marked as false if we are disconnecting with no client
		c.metrics.UpdateConnectionStatus(false)
	}
}

func (c *client) onConnect(client mqtt.Client) {
	log := GetLogger()
	log.Info("Connected to MQTT broker",
		logger.String("broker", c.config.Broker),
		logger.String("client_id", c.config.ClientID))
	c.metrics.UpdateConnectionStatus(true)

	// Publish MQTT connected alert event
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeIntegration,
		EventName:  alerting.EventMQTTConnected,
		Properties: map[string]any{
			alerting.PropertyBroker: c.config.Broker,
		},
	})

	// Publish online status if LWT is enabled
	if c.config.LWT.Enabled && c.config.LWT.Topic != "" {
		token := client.Publish(c.config.LWT.Topic, c.config.LWT.QoS, true, "online")
		if token.WaitTimeout(c.config.PublishTimeout) && token.Error() == nil {
			log.Debug("Published online status to LWT topic",
				logger.String("topic", c.config.LWT.Topic))
		} else {
			log.Warn("Failed to publish online status to LWT topic",
				logger.String("topic", c.config.LWT.Topic),
				logger.Error(token.Error()))
		}
	}

	// Call registered OnConnect handlers
	c.mu.RLock()
	handlers := make([]OnConnectHandler, len(c.onConnectHandlers))
	copy(handlers, c.onConnectHandlers)
	c.mu.RUnlock()

	for i, handler := range handlers {
		log.Debug("Executing OnConnect handler", logger.Int("handler_index", i))
		// Run handlers in goroutines to avoid blocking the paho callback
		go func(h OnConnectHandler, idx int) {
			defer func() {
				if r := recover(); r != nil {
					log.Error("OnConnect handler panicked",
						logger.Int("handler_index", idx),
						logger.Any("panic", r))
				}
			}()
			h()
		}(handler, i)
	}
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

	GetLogger().Error("Connection to MQTT broker lost",
		logger.String("broker", c.config.Broker),
		logger.String("client_id", c.config.ClientID),
		logger.Error(enhancedErr))
	c.metrics.UpdateConnectionStatus(false)
	c.metrics.IncrementErrorsWithCategory("mqtt-connection", "connection_lost")

	// Publish MQTT disconnected alert event
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeIntegration,
		EventName:  alerting.EventMQTTDisconnected,
		Properties: map[string]any{
			alerting.PropertyBroker: c.config.Broker,
		},
	})

	// Send notification for connection lost
	notification.NotifyIntegrationFailure("MQTT", enhancedErr)
	// Check if we should attempt to reconnect or if Disconnect was called
	select {
	case <-c.reconnectStop:
		GetLogger().Info("Reconnect mechanism stopped, not attempting reconnect")
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
		GetLogger().Debug("Reconnect timer already active, stopping previous one")
		c.reconnectTimer.Stop()
	}

	reconnectDelay := c.config.ReconnectDelay
	GetLogger().Info("Starting reconnect timer", logger.Duration("delay", reconnectDelay))
	c.reconnectTimer = time.AfterFunc(reconnectDelay, func() {
		select {
		case <-c.reconnectStop: // Check if disconnect was called before timer fired
			GetLogger().Info("Reconnect cancelled before execution")
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

	log := GetLogger().With(
		logger.String("broker", c.config.Broker),
		logger.String("client_id", c.config.ClientID))
	log.Info("Attempting to reconnect to MQTT broker")

	// Check if reconnect process was stopped before attempting connection
	select {
	case <-c.reconnectStop:
		log.Info("Reconnect mechanism stopped during backoff, aborting reconnect attempt")
		return
	default:
		// Proceed with connect attempt
	}

	// Use connectWithOptions with isAutoReconnect=true to bypass cooldown check
	if err := c.connectWithOptions(ctx, true); err != nil {
		log.Error("Reconnect attempt failed", logger.Error(err))

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
			log.Info("Reconnect mechanism stopped after failed attempt, not rescheduling")
			return
		default:
			// Schedule next attempt
			c.startReconnectTimer() // Reschedule another attempt after delay
		}
	} else {
		// Connection successful, logged by onConnect
		log.Info("Reconnect successful")
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
	GetLogger().Debug("CA certificate loaded", logger.String("path", c.config.TLS.CACert))
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
	GetLogger().Debug("Client certificate loaded",
		logger.String("cert_path", c.config.TLS.ClientCert),
		logger.String("key_path", c.config.TLS.ClientKey))
	return nil
}
