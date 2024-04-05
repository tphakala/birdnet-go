package mqtt

import (
	"errors"
	"log"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/tphakala/birdnet-go/internal/conf"
)

type Client struct {
	Settings       *conf.Settings
	internalClient mqtt.Client
}

func New(settings *conf.Settings) (*Client, error) {
   if settings == nil || settings.Realtime.MQTT.Broker == "" {
       return nil, errors.New("invalid MQTT settings provided")
   }
	return &Client{
		Settings: settings,
	}, nil
}

// Connect to MQTT broker
func (c *Client) Connect() error {
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

	// It will wait infinitely until the connection is established
	if token := client.Connect(); token.Wait() && token.Error() != nil {
	   log.Printf("Failed to connect to MQTT broker: %s", token.Error())
	   return errors.New("failed to connect to MQTT broker")
	}

	return nil
}

// Publish a message to a topic
func (c *Client) Publish(topic string, payload string) error {
	if c.internalClient == nil {
		return errors.New("MQTT client is not initialized")
	}

	if c.internalClient.IsConnected() == false {
		return errors.New("MQTT client is not connected")
	}

	token := c.internalClient.Publish(topic, 0, false, payload)
	token.Wait()
	return token.Error()
}

func (c *Client) onConnect(client mqtt.Client) {
	log.Printf("Connected to MQTT broker: %s", c.Settings.Realtime.MQTT.Broker)
}

func (c *Client) onConnectionLost(client mqtt.Client, err error) {
	log.Printf("Connection to MQTT broker lost: %s, error: %v", c.Settings.Realtime.MQTT.Broker, err)
}
