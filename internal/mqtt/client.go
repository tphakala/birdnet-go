// client.go
package mqtt

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// client implements the Client interface.
type client struct {
	config          Config
	internalClient  mqtt.Client
	lastConnAttempt time.Time
	mu              sync.Mutex
	reconnectTimer  *time.Timer
	reconnectStop   chan struct{}
}

// NewClient creates a new MQTT client with the provided configuration.
func NewClient(settings *conf.Settings) Client {
	return &client{
		config: Config{
			Broker:   settings.Realtime.MQTT.Broker,
			ClientID: "birdnet-go",
			Username: settings.Realtime.MQTT.Username,
			Password: settings.Realtime.MQTT.Password,
		},
		reconnectStop: make(chan struct{}),
	}
}

// Connect attempts to establish a connection to the MQTT broker.
// It first resolves the broker's hostname and then attempts to connect.
func (c *client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.lastConnAttempt) < 1*time.Minute {
		return fmt.Errorf("connection attempt too recent")
	}
	c.lastConnAttempt = time.Now()

	// Resolve the broker's hostname
	if err := c.resolveBrokerHostname(); err != nil {
		return fmt.Errorf("failed to resolve broker hostname: %w", err)
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

	return nil
}

// resolveBrokerHostname attempts to resolve the hostname of the MQTT broker.
// It returns an error if the resolution fails.
func (c *client) resolveBrokerHostname() error {
	u, err := url.Parse(c.config.Broker)
	if err != nil {
		return fmt.Errorf("invalid broker URL: %w", err)
	}

	host := u.Hostname()
	_, err = net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("failed to resolve hostname %s: %w", host, err)
	}

	return nil
}

// Publish sends a message to the specified topic on the MQTT broker.
func (c *client) Publish(ctx context.Context, topic string, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.IsConnected() {
		return fmt.Errorf("not connected to MQTT broker")
	}

	token := c.internalClient.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("publish timeout")
	}
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
	}
	if c.reconnectTimer != nil {
		c.reconnectTimer.Stop()
	}
	close(c.reconnectStop)
}

func (c *client) onConnect(client mqtt.Client) {
	fmt.Printf("Connected to MQTT broker: %s\n", c.config.Broker)
}

func (c *client) onConnectionLost(client mqtt.Client, err error) {
	fmt.Printf("Connection to MQTT broker lost: %s, error: %v\n", c.config.Broker, err)
	c.startReconnectTimer()
}

func (c *client) startReconnectTimer() {
	c.reconnectTimer = time.AfterFunc(time.Minute, func() {
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := c.Connect(ctx)
		cancel()

		if err == nil {
			fmt.Println("Successfully reconnected to MQTT broker")
			c.startReconnectTimer()
			return
		}

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
