package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/tphakala/birdnet-go/internal/conf"
)

type Client struct {
	Settings        *conf.Settings
	internalClient  mqtt.Client
	lastConnAttempt time.Time
	mu              sync.Mutex
}

func New(settings *conf.Settings) *Client {
	return &Client{
		Settings: settings,
	}
}

// Resolve hostname to IP address
func (c *Client) resolveHostname(hostname string) error {
	_, err := net.LookupHost(hostname)
	return err
}

// Connect to MQTT broker
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if we've tried to connect recently
	if time.Since(c.lastConnAttempt) < 1*time.Minute {
		return errors.New("connection attempt too recent")
	}
	c.lastConnAttempt = time.Now()

	// Resolve hostname
	if err := c.resolveHostname(c.Settings.Realtime.MQTT.Broker); err != nil {
		return fmt.Errorf("failed to resolve broker hostname: %w", err)
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.Settings.Realtime.MQTT.Broker)
	opts.SetClientID("birdnet-go")
	opts.SetUsername(c.Settings.Realtime.MQTT.Username)
	opts.SetPassword(c.Settings.Realtime.MQTT.Password)
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(c.onConnect)
	opts.SetConnectionLostHandler(c.onConnectionLost)
	opts.SetConnectRetry(true)

	client := mqtt.NewClient(opts)
	c.internalClient = client

	connectToken := client.Connect()
	if !connectToken.WaitTimeout(30 * time.Second) {
		return errors.New("connection timeout")
	}
	if err := connectToken.Error(); err != nil {
		return fmt.Errorf("connection error: %w", err)
	}

	return nil
}

// Publish a message to a topic
func (c *Client) Publish(ctx context.Context, topic string, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.EnsureConnection(ctx); err != nil {
		return fmt.Errorf("failed to ensure connection: %w", err)
	}

	publishToken := c.internalClient.Publish(topic, 0, false, payload)
	if !publishToken.WaitTimeout(10 * time.Second) {
		return errors.New("publish timeout")
	}
	return publishToken.Error()
}

// EnsureConnection ensures that the client is connected to the broker
func (c *Client) EnsureConnection(ctx context.Context) error {
	if c.internalClient == nil || !c.internalClient.IsConnected() {
		log.Println("MQTT connection not established, attempting to connect")
		return c.Connect(ctx)
	}
	return nil
}

func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("Connected to MQTT broker: %s", c.Settings.Realtime.MQTT.Broker)
}

func (c *Client) onConnectionLost(client mqtt.Client, err error) {
	log.Printf("Connection to MQTT broker lost: %s, error: %v", c.Settings.Realtime.MQTT.Broker, err)
}

// IsConnected returns true if the client is connected to the broker
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.internalClient != nil && c.internalClient.IsConnected()
}
