// mqtt.go: MQTT-related functionality for the processor
package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

const (
	// mqttConnectionTimeout is the timeout for MQTT connection attempts
	mqttConnectionTimeout = 30 * time.Second
	// discoveryPublishTimeout is the timeout for publishing discovery messages
	discoveryPublishTimeout = 30 * time.Second
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
	client := p.MqttClient
	p.MqttClient = nil
	p.mqttMutex.Unlock()

	if client != nil {
		client.Disconnect()
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
	if settings == nil {
		return
	}
	if !settings.Realtime.MQTT.Enabled {
		return
	}

	log := GetLogger()
	// Create a new MQTT client using the settings and metrics
	mqttClient, err := mqtt.NewClient(settings, p.Metrics)
	if err != nil {
		log.Error("failed to create MQTT client", logger.Error(err))
		return
	}

	// Register Home Assistant discovery handler if enabled
	if settings.Realtime.MQTT.HomeAssistant.Enabled {
		p.registerHomeAssistantDiscovery(mqttClient, settings)
	}

	// Create a context with a timeout for the connection attempt
	ctx, cancel := context.WithTimeout(context.Background(), mqttConnectionTimeout)
	defer cancel() // Ensure the cancel function is called to release resources

	// Attempt to connect to the MQTT broker
	if err := mqttClient.Connect(ctx); err != nil {
		log.Error("failed to connect to MQTT broker", logger.Error(err))
		return
	}

	// Set the client only if connection was successful
	p.SetMQTTClient(mqttClient)
}

// RegisterHomeAssistantDiscovery registers the OnConnect handler for Home Assistant discovery.
// This is called during MQTT initialization and after MQTT reconfiguration.
func (p *Processor) RegisterHomeAssistantDiscovery(client mqtt.Client, settings *conf.Settings) {
	if client == nil || settings == nil {
		return
	}
	if !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}
	p.registerHomeAssistantDiscovery(client, settings)
}

// registerHomeAssistantDiscovery registers the OnConnect handler for Home Assistant discovery.
func (p *Processor) registerHomeAssistantDiscovery(client mqtt.Client, settings *conf.Settings) {
	log := GetLogger()

	// Create discovery configuration
	haSettings := settings.Realtime.MQTT.HomeAssistant
	discoveryConfig := mqtt.DiscoveryConfig{
		DiscoveryPrefix: haSettings.DiscoveryPrefix,
		BaseTopic:       settings.Realtime.MQTT.Topic,
		DeviceName:      haSettings.DeviceName,
		NodeID:          settings.Main.Name,
		Version:         settings.Version,
	}

	// Create the discovery publisher
	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)

	// Register the OnConnect handler
	client.RegisterOnConnectHandler(func() {
		log.Info("MQTT connected, publishing Home Assistant discovery messages")

		// Get audio sources from the registry
		sources := p.getAudioSourcesForDiscovery()

		// Create a context for publishing
		ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
		defer cancel()

		// Publish discovery messages
		if err := publisher.PublishDiscovery(ctx, sources, settings); err != nil {
			log.Error("Failed to publish Home Assistant discovery",
				logger.Error(err))
		}
	})

	log.Info("Home Assistant discovery handler registered",
		logger.String("discovery_prefix", haSettings.DiscoveryPrefix),
		logger.String("device_name", haSettings.DeviceName))
}

// TriggerHomeAssistantDiscovery manually triggers Home Assistant discovery messages.
// This can be called from the API to force republishing of discovery messages.
func (p *Processor) TriggerHomeAssistantDiscovery(ctx context.Context) error {
	log := GetLogger()

	// Check if MQTT is enabled and Home Assistant discovery is enabled
	if !p.Settings.Realtime.MQTT.Enabled {
		return fmt.Errorf("MQTT is not enabled")
	}
	if !p.Settings.Realtime.MQTT.HomeAssistant.Enabled {
		return fmt.Errorf("home assistant discovery is not enabled")
	}

	// Get the MQTT client
	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		return fmt.Errorf("MQTT client not connected")
	}

	// Create discovery configuration
	haSettings := p.Settings.Realtime.MQTT.HomeAssistant
	discoveryConfig := mqtt.DiscoveryConfig{
		DiscoveryPrefix: haSettings.DiscoveryPrefix,
		BaseTopic:       p.Settings.Realtime.MQTT.Topic,
		DeviceName:      haSettings.DeviceName,
		NodeID:          p.Settings.Main.Name,
		Version:         p.Settings.Version,
	}

	// Create the discovery publisher
	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)

	// Get audio sources
	sources := p.getAudioSourcesForDiscovery()

	log.Info("Manually triggering Home Assistant discovery",
		logger.Int("source_count", len(sources)))

	// Publish discovery messages with timeout
	publishCtx, cancel := context.WithTimeout(ctx, discoveryPublishTimeout)
	defer cancel()

	if err := publisher.PublishDiscovery(publishCtx, sources, p.Settings); err != nil {
		log.Error("Failed to publish Home Assistant discovery", logger.Error(err))
		return fmt.Errorf("failed to publish discovery: %w", err)
	}

	return nil
}

// getAudioSourcesForDiscovery retrieves audio sources from the registry for HA discovery.
func (p *Processor) getAudioSourcesForDiscovery() []datastore.AudioSource {
	registry := myaudio.GetRegistry()
	registrySources := registry.ListSources()

	// Convert myaudio.AudioSource to datastore.AudioSource
	sources := make([]datastore.AudioSource, 0, len(registrySources))
	for _, src := range registrySources {
		sources = append(sources, datastore.AudioSource{
			ID:          src.ID,
			SafeString:  src.SafeString,
			DisplayName: src.DisplayName,
		})
	}

	// If no sources registered yet, create a default source
	if len(sources) == 0 {
		sources = append(sources, datastore.AudioSource{
			ID:          "default",
			DisplayName: "Default",
		})
	}

	return sources
}
