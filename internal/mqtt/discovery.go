// discovery.go: Home Assistant MQTT auto-discovery implementation.
// See: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Sensor type constants to avoid magic strings
const (
	SensorSpecies        = "species"
	SensorConfidence     = "confidence"
	SensorScientificName = "scientific_name"
	SensorSoundLevel     = "sound_level"
)

// deviceIDPrefix is the standard prefix for all BirdNET-Go device identifiers
const deviceIDPrefix = "birdnet_go"

// AllSensorTypes lists all sensor types for iteration (e.g., during removal)
var AllSensorTypes = []string{
	SensorSpecies,
	SensorConfidence,
	SensorScientificName,
	SensorSoundLevel,
}

// idSanitizer replaces invalid characters in IDs with underscores.
// Home Assistant requires IDs to contain only [a-zA-Z0-9_-].
var idSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// maxDisplayNameLength is the maximum length for display names in Home Assistant.
// This keeps entity names manageable in the HA UI.
const maxDisplayNameLength = 32

// SanitizeID ensures the ID contains only valid characters for MQTT topics and HA entity IDs.
func SanitizeID(id string) string {
	// Replace invalid characters with underscores
	sanitized := idSanitizer.ReplaceAllString(id, "_")
	// Remove consecutive underscores
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}
	// Trim leading/trailing underscores
	sanitized = strings.Trim(sanitized, "_")
	// Ensure non-empty result
	if sanitized == "" {
		sanitized = "unknown"
	}
	return sanitized
}

// shortenDisplayName ensures display names stay within maxDisplayNameLength.
// This prevents excessively long entity names in Home Assistant when
// source IDs (like RTSP URLs) are used as fallback display names.
func shortenDisplayName(name string) string {
	if len(name) <= maxDisplayNameLength {
		return name
	}

	// For RTSP-style IDs (rtsp_xxxxx...), keep the prefix and truncate
	if strings.HasPrefix(name, "rtsp_") && len(name) > 13 {
		// Keep "rtsp_" + first 8 chars of UUID = 13 chars
		// Example: "rtsp_a1b2c3d4-e5f6..." -> "rtsp_a1b2c3d4"
		return name[:13]
	}

	// For URLs or other long strings, truncate intelligently
	// Try to cut at a natural boundary (underscore, hyphen, slash)
	truncated := name[:maxDisplayNameLength]

	// Find the last natural break point
	lastBreak := -1
	for i := len(truncated) - 1; i >= maxDisplayNameLength/2; i-- {
		if truncated[i] == '_' || truncated[i] == '-' || truncated[i] == '/' {
			lastBreak = i
			break
		}
	}

	if lastBreak > 0 {
		return truncated[:lastBreak]
	}

	return truncated
}

// DiscoveryPayload represents a Home Assistant MQTT discovery message.
type DiscoveryPayload struct {
	Name                string           `json:"name"`
	UniqueID            string           `json:"unique_id"`
	StateTopic          string           `json:"state_topic"`
	ValueTemplate       string           `json:"value_template,omitempty"`
	UnitOfMeasurement   string           `json:"unit_of_measurement,omitempty"`
	DeviceClass         string           `json:"device_class,omitempty"`
	StateClass          string           `json:"state_class,omitempty"`
	Icon                string           `json:"icon,omitempty"`
	EntityCategory      string           `json:"entity_category,omitempty"`
	PayloadAvailable    string           `json:"payload_available,omitempty"`
	PayloadNotAvailable string           `json:"payload_not_available,omitempty"`
	AvailabilityTopic   string           `json:"availability_topic,omitempty"`
	Device              DiscoveryDevice  `json:"device"`
	Origin              *DiscoveryOrigin `json:"origin,omitempty"`
}

// DiscoveryDevice represents the device information in a discovery payload.
type DiscoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SWVersion    string   `json:"sw_version,omitempty"`
	ViaDevice    string   `json:"via_device,omitempty"`
}

// DiscoveryOrigin provides information about the software creating the discovery message.
type DiscoveryOrigin struct {
	Name       string `json:"name"`
	SWVersion  string `json:"sw_version,omitempty"`
	SupportURL string `json:"support_url,omitempty"`
}

// DiscoveryConfig holds configuration for generating discovery payloads.
type DiscoveryConfig struct {
	DiscoveryPrefix string // Home Assistant discovery topic prefix (default: homeassistant)
	BaseTopic       string // Base MQTT topic for state messages (e.g., birdnet)
	DeviceName      string // Base name for devices (e.g., BirdNET-Go)
	NodeID          string // Node identifier (typically main.name from config)
	Version         string // Software version
}

// Publisher handles publishing Home Assistant discovery messages.
type Publisher struct {
	client Client
	config DiscoveryConfig
}

// NewDiscoveryPublisher creates a new discovery publisher.
func NewDiscoveryPublisher(client Client, config *DiscoveryConfig) *Publisher {
	return &Publisher{
		client: client,
		config: *config,
	}
}

// PublishDiscovery publishes Home Assistant discovery configs for all sources.
func (p *Publisher) PublishDiscovery(ctx context.Context, sources []datastore.AudioSource, settings *conf.Settings) error {
	log := GetLogger()
	log.Info("Publishing Home Assistant discovery messages",
		logger.Int("source_count", len(sources)),
		logger.String("discovery_prefix", p.config.DiscoveryPrefix))

	// Publish bridge device first
	if err := p.publishBridgeDiscovery(ctx); err != nil {
		log.Error("Failed to publish bridge discovery", logger.Error(err))
		return err
	}

	// Publish discovery for each audio source, tracking first error
	var firstErr error
	for _, source := range sources {
		if err := p.publishSourceDiscovery(ctx, source, settings); err != nil {
			log.Error("Failed to publish source discovery",
				logger.String("source_id", source.ID),
				logger.Error(err))
			if firstErr == nil {
				firstErr = err
			}
			// Continue with other sources even if one fails
		}
	}

	if firstErr != nil {
		return fmt.Errorf("failed to publish discovery for one or more sources: %w", firstErr)
	}

	log.Info("Home Assistant discovery messages published successfully")
	return nil
}

// publishBridgeDiscovery publishes the bridge device discovery.
func (p *Publisher) publishBridgeDiscovery(ctx context.Context) error {
	nodeID := SanitizeID(p.config.NodeID)
	bridgeID := p.bridgeID(nodeID)

	payload := DiscoveryPayload{
		Name:                "Status",
		UniqueID:            bridgeID + "_status",
		StateTopic:          p.config.BaseTopic + "/status",
		DeviceClass:         "connectivity",
		EntityCategory:      "diagnostic",
		PayloadAvailable:    "online",
		PayloadNotAvailable: "offline",
		Device: DiscoveryDevice{
			Identifiers:  []string{bridgeID},
			Name:         p.config.DeviceName,
			Manufacturer: "BirdNET-Go",
			Model:        "Bridge",
			SWVersion:    p.config.Version,
		},
		Origin: p.defaultOrigin(),
	}

	topic := p.getBridgeTopic(nodeID)
	return p.publishPayload(ctx, topic, &payload)
}

// publishSourceDiscovery publishes discovery for a specific audio source.
func (p *Publisher) publishSourceDiscovery(ctx context.Context, source datastore.AudioSource, settings *conf.Settings) error {
	nodeID := SanitizeID(p.config.NodeID)
	sourceID := SanitizeID(source.ID)
	deviceID := fmt.Sprintf("%s_%s_%s", deviceIDPrefix, nodeID, sourceID)
	bridgeID := p.bridgeID(nodeID)

	// Determine display name
	// Use shortenDisplayName when falling back to source.ID to prevent
	// excessively long device names (e.g., from RTSP URLs or UUIDs)
	displayName := source.DisplayName
	if displayName == "" {
		displayName = shortenDisplayName(source.ID)
	}
	deviceName := fmt.Sprintf("%s %s", p.config.DeviceName, displayName)

	// Common device info for all sensors of this source
	device := DiscoveryDevice{
		Identifiers:  []string{deviceID},
		Name:         deviceName,
		Manufacturer: "BirdNET-Go",
		Model:        "Audio Analyzer",
		SWVersion:    p.config.Version,
		ViaDevice:    bridgeID,
	}

	availabilityTopic := p.config.BaseTopic + "/status"

	// Publish Last Species sensor
	// Note: ValueTemplate uses source.ID (not sanitized) to match incoming JSON
	if err := p.publishSensor(ctx, nodeID, sourceID, SensorSpecies, &DiscoveryPayload{
		Name:              "Last Species",
		UniqueID:          deviceID + "_species",
		StateTopic:        p.config.BaseTopic,
		ValueTemplate:     fmt.Sprintf("{{ value_json.CommonName if value_json.sourceId == '%s' else this.state }}", source.ID),
		Icon:              "mdi:bird",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Confidence sensor
	// Note: ValueTemplate uses source.ID (not sanitized) to match incoming JSON
	if err := p.publishSensor(ctx, nodeID, sourceID, SensorConfidence, &DiscoveryPayload{
		Name:              "Confidence",
		UniqueID:          deviceID + "_confidence",
		StateTopic:        p.config.BaseTopic,
		ValueTemplate:     fmt.Sprintf("{{ (value_json.Confidence * 100) | round(1) if value_json.sourceId == '%s' else this.state }}", source.ID),
		UnitOfMeasurement: "%",
		StateClass:        "measurement",
		Icon:              "mdi:percent",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Scientific Name sensor
	// Note: ValueTemplate uses source.ID (not sanitized) to match incoming JSON
	if err := p.publishSensor(ctx, nodeID, sourceID, SensorScientificName, &DiscoveryPayload{
		Name:              "Scientific Name",
		UniqueID:          deviceID + "_scientific_name",
		StateTopic:        p.config.BaseTopic,
		ValueTemplate:     fmt.Sprintf("{{ value_json.ScientificName if value_json.sourceId == '%s' else this.state }}", source.ID),
		Icon:              "mdi:format-quote-close",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Sound Level sensor if sound level monitoring is enabled
	// Note: ValueTemplate uses source.ID (not sanitized) to match incoming JSON
	// Band key format: formatBandKey() in soundlevel.go produces "1.0_kHz" for 1000 Hz
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := p.publishSensor(ctx, nodeID, sourceID, SensorSoundLevel, &DiscoveryPayload{
			Name:              "Sound Level",
			UniqueID:          deviceID + "_sound_level",
			StateTopic:        p.config.BaseTopic + "/soundlevel",
			ValueTemplate:     fmt.Sprintf("{{ value_json.b['1.0_kHz'].m if value_json.src == '%s' else this.state }}", source.ID),
			UnitOfMeasurement: "dB",
			DeviceClass:       "sound_pressure",
			StateClass:        "measurement",
			Icon:              "mdi:volume-high",
			AvailabilityTopic: availabilityTopic,
			Device:            device,
		}); err != nil {
			return err
		}
	}

	return nil
}

// publishSensor publishes a single sensor discovery message.
func (p *Publisher) publishSensor(ctx context.Context, nodeID, sourceID, sensorType string, payload *DiscoveryPayload) error {
	// Add origin if not set
	if payload.Origin == nil {
		payload.Origin = p.defaultOrigin()
	}

	topic := p.getSensorTopic(nodeID, sourceID, sensorType)
	return p.publishPayload(ctx, topic, payload)
}

// publishPayload marshals and publishes a discovery payload.
func (p *Publisher) publishPayload(ctx context.Context, topic string, payload *DiscoveryPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal discovery payload: %w", err)
	}

	GetLogger().Debug("Publishing discovery message",
		logger.String("topic", topic),
		logger.Int("payload_size", len(data)))

	// Discovery messages must be retained
	return p.client.PublishWithRetain(ctx, topic, string(data), true)
}

// getBridgeTopic constructs the MQTT discovery topic for the bridge device.
func (p *Publisher) getBridgeTopic(nodeID string) string {
	return fmt.Sprintf("%s/binary_sensor/%s/status/config", p.config.DiscoveryPrefix, nodeID)
}

// getSensorTopic constructs the MQTT discovery topic for a specific sensor.
func (p *Publisher) getSensorTopic(nodeID, sourceID, sensorType string) string {
	objectID := fmt.Sprintf("%s_%s_%s", nodeID, sourceID, sensorType)
	return fmt.Sprintf("%s/sensor/%s/%s/config", p.config.DiscoveryPrefix, nodeID, objectID)
}

// defaultOrigin returns the standard origin block for discovery payloads.
func (p *Publisher) defaultOrigin() *DiscoveryOrigin {
	return &DiscoveryOrigin{
		Name:       "BirdNET-Go",
		SWVersion:  p.config.Version,
		SupportURL: "https://github.com/tphakala/birdnet-go",
	}
}

// bridgeID returns the standardized bridge device identifier.
func (p *Publisher) bridgeID(nodeID string) string {
	return fmt.Sprintf("%s_%s_bridge", deviceIDPrefix, nodeID)
}

// RemoveDiscovery publishes empty payloads to remove all discovery entries.
func (p *Publisher) RemoveDiscovery(ctx context.Context, sources []datastore.AudioSource) error {
	log := GetLogger()
	log.Info("Removing Home Assistant discovery messages")

	nodeID := SanitizeID(p.config.NodeID)

	// Remove bridge
	bridgeTopic := p.getBridgeTopic(nodeID)
	if err := p.client.PublishWithRetain(ctx, bridgeTopic, "", true); err != nil {
		log.Warn("Failed to remove bridge discovery", logger.Error(err))
	}

	// Remove each source's sensors
	for _, source := range sources {
		sourceID := SanitizeID(source.ID)

		for _, sensorType := range AllSensorTypes {
			topic := p.getSensorTopic(nodeID, sourceID, sensorType)
			if err := p.client.PublishWithRetain(ctx, topic, "", true); err != nil {
				log.Warn("Failed to remove sensor discovery",
					logger.String("topic", topic),
					logger.Error(err))
			}
		}
	}

	return nil
}
