// client.go: Package mqtt provides an abstraction for MQTT client functionality.
package mqtt

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

// client implements the Client interface.
type client struct {
	config          Config
	internalClient  mqtt.Client
	lastConnAttempt time.Time
	mu              sync.Mutex
	reconnectTimer  *time.Timer
	reconnectStop   chan struct{}
	metrics         *metrics.MQTTMetrics
}

// NewClient creates a new MQTT client with the provided configuration.
func NewClient(settings *conf.Settings, metrics *telemetry.Metrics) (Client, error) {
	return &client{
		config: Config{
			Broker:            settings.Realtime.MQTT.Broker,
			ClientID:          settings.Main.Name,
			Username:          settings.Realtime.MQTT.Username,
			Password:          settings.Realtime.MQTT.Password,
			ReconnectCooldown: 5 * time.Second,
			ReconnectDelay:    1 * time.Second,
		},
		reconnectStop: make(chan struct{}),
		metrics:       metrics.MQTT,
	}, nil
}

// Connect attempts to establish a connection to the MQTT broker.
// It first resolves the broker's hostname and then attempts to connect.
func (c *client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastConnAttempt) < c.config.ReconnectCooldown {
		return fmt.Errorf("connection attempt too recent, last attempt was %v ago", time.Since(c.lastConnAttempt))
	}
	c.lastConnAttempt = time.Now()

	// Parse the broker URL
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		return fmt.Errorf("invalid broker URL: %w", err)
	}

	host := u.Hostname()

	// Check if the host is an IP address
	if net.ParseIP(host) == nil {
		// It's not an IP address, so attempt to resolve it
		_, err = net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			// If it's a DNS error, return it directly
			if dnsErr, ok := err.(*net.DNSError); ok {
				return dnsErr
			}
			// For other errors, wrap it
			return fmt.Errorf("failed to resolve hostname %s: %w", host, err)
		}
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.config.Broker)
	opts.SetClientID(c.config.ClientID)
	opts.SetUsername(c.config.Username)
	opts.SetPassword(c.config.Password)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)
	opts.SetConnectRetry(true)

	c.internalClient = mqtt.NewClient(opts)

	token := c.internalClient.Connect()
	if !token.WaitTimeout(30 * time.Second) {
		return fmt.Errorf("connection timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("connection error: %w", err)
	}

	// Update connection status metric
	c.metrics.UpdateConnectionStatus(true)

	return nil
}

// Publish sends a message to the specified topic on the MQTT broker.
func (c *client) Publish(ctx context.Context, topic string, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Printf("Publishing to topic %s\n", topic)

	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	timer := c.metrics.StartPublishTimer()
	defer timer.ObserveDuration()

	token := c.internalClient.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		log.Printf("Publish timeout for topic %s\n", topic)
		return fmt.Errorf("publish timeout")
	}

	c.metrics.IncrementMessagesDelivered()
	c.metrics.ObserveMessageSize(float64(len(payload)))

	return token.Error()
}

// IsConnected returns true if the client is currently connected to the MQTT broker.
func (c *client) IsConnected() bool {
	return c.internalClient != nil && c.internalClient.IsConnected()
}

// Disconnect closes the connection to the MQTT broker.
func (c *client) Disconnect() {
	if c.internalClient != nil && c.internalClient.IsConnected() {
		c.internalClient.Disconnect(250)
		c.metrics.UpdateConnectionStatus(false)
	}
	if c.reconnectTimer != nil {
		c.reconnectTimer.Stop()
	}
	close(c.reconnectStop)
}

func (c *client) onConnect(client mqtt.Client) {
	fmt.Printf("Connected to MQTT broker: %s\n", c.config.Broker)
	c.metrics.UpdateConnectionStatus(true)
}

func (c *client) onConnectionLost(client mqtt.Client, err error) {
	fmt.Printf("Connection to MQTT broker lost: %s, error: %v\n", c.config.Broker, err)
	c.metrics.UpdateConnectionStatus(false)
	c.metrics.IncrementErrors()
	c.startReconnectTimer()
}

func (c *client) startReconnectTimer() {
	c.reconnectTimer = time.AfterFunc(c.config.ReconnectDelay, func() {
		select {
		case <-c.reconnectStop:
			return
		default:
			c.reconnectWithBackoff()
		}
	})
}

func (c *client) reconnectWithBackoff() {
	backoff := time.Second
	maxBackoff := 5 * time.Minute

	for {
		c.metrics.IncrementReconnectAttempts()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := c.Connect(ctx)
		cancel()

		if err == nil {
			fmt.Println("Successfully reconnected to MQTT broker")
			return
		}

		c.metrics.IncrementErrors()

		fmt.Printf("Failed to reconnect to MQTT broker: %s\n", err)
		fmt.Printf("Retrying in %v\n", backoff)

		select {
		case <-time.After(backoff):
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		case <-c.reconnectStop:
			return
		}
	}
}
