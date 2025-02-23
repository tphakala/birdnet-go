// mqtt.go: MQTT-related functionality for the processor
package processor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// GetMQTTClient safely returns the current MQTT client
func (p *Processor) GetMQTTClient() mqtt.Client {
	p.mqttMutex.RLock()
	defer p.mqttMutex.RUnlock()
	return p.MqttClient
}

// SetMQTTClient safely sets a new MQTT client
func (p *Processor) SetMQTTClient(client mqtt.Client) {
	p.mqttMutex.Lock()
	defer p.mqttMutex.Unlock()
	p.MqttClient = client
}

// DisconnectMQTTClient safely disconnects and removes the MQTT client
func (p *Processor) DisconnectMQTTClient() {
	p.mqttMutex.Lock()
	defer p.mqttMutex.Unlock()
	if p.MqttClient != nil {
		p.MqttClient.Disconnect()
		p.MqttClient = nil
	}
}

// PublishMQTT safely publishes a message using the MQTT client if available
func (p *Processor) PublishMQTT(ctx context.Context, topic, payload string) error {
	p.mqttMutex.RLock()
	client := p.MqttClient
	p.mqttMutex.RUnlock()

	if client != nil && client.IsConnected() {
		return client.Publish(ctx, topic, payload)
	}
	return fmt.Errorf("MQTT client not available or not connected")
}

// initializeMQTT initializes the MQTT client if enabled in settings
func (p *Processor) initializeMQTT(settings *conf.Settings) {
	if !settings.Realtime.MQTT.Enabled {
		return
	}

	// Create a new MQTT client using the settings and metrics
	mqttClient, err := mqtt.NewClient(settings, p.Metrics)
	if err != nil {
		// Log an error if client creation fails
		log.Printf("failed to create MQTT client: %s", err)
		return
	}

	// Create a context with a 30-second timeout for the connection attempt
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel() // Ensure the cancel function is called to release resources

	// Attempt to connect to the MQTT broker
	if err := mqttClient.Connect(ctx); err != nil {
		// Log an error if the connection attempt fails
		log.Printf("failed to connect to MQTT broker: %s", err)
		return
	}

	// Set the client only if connection was successful
	p.SetMQTTClient(mqttClient)
}
