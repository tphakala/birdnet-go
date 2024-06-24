package mqtt

import (
	"context"
	"errors"
	"log"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/tphakala/birdnet-go/internal/conf"
)

type Client struct {
	Settings       *conf.Settings
	internalClient mqtt.Client
}

func New(settings *conf.Settings) *Client {
	return &Client{
		Settings: settings,
	}
}

// Connect to MQTT broker with a timeout
func (c *Client) Connect(ctx context.Context) error {
	if c.internalClient != nil && c.internalClient.IsConnected() {
		return errors.New("already connected")
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

	// Use a select statement to implement the timeout
	select {
	case <-connectToken.Done():
		if connectToken.Error() != nil {
			log.Printf("Failed to connect to MQTT broker: %s", connectToken.Error())
			return errors.New("failed to connect to MQTT broker")
		}
		return nil
	case <-ctx.Done():
		return errors.New("connection timeout")
	}
}

// Publish a message to a topic
func (c *Client) Publish(ctx context.Context, topic string, payload string) error {
	if c.internalClient == nil {
		return errors.New("MQTT client is not initialized")
	}

	if !c.internalClient.IsConnected() {
		return errors.New("MQTT client is not connected")
	}

	publishToken := c.internalClient.Publish(topic, 0, false, payload)

	// Use a select statement to implement the timeout
	select {
	case <-publishToken.Done():
		return publishToken.Error()
	case <-ctx.Done():
		return errors.New("publish timeout")
	}
}

func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("Connected to MQTT broker: %s", c.Settings.Realtime.MQTT.Broker)
}

func (c *Client) onConnectionLost(client mqtt.Client, err error) {
	log.Printf("Connection to MQTT broker lost: %s, error: %v", c.Settings.Realtime.MQTT.Broker, err)
}
