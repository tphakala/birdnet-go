// discovery.go: Home Assistant MQTT auto-discovery implementation.
// See: https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// Status payload constants for MQTT availability and state
const (
	StatusPayloadOnline  = "online"
	StatusPayloadOffline = "offline"
)

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

// getSourceID returns the sanitized source identifier for MQTT topics and entity IDs.
// Prefers DisplayName when available for user-friendly entity IDs like "mediamtx_streamer_species"
// instead of internal IDs like "rtsp_65c31a0b_species".
func getSourceID(source datastore.AudioSource) string {
	if source.DisplayName != "" {
		return SanitizeID(source.DisplayName)
	}
	return SanitizeID(source.ID)
}

// SourceEntityKeys maps each source's raw ID to the entity key used for its HA
// device identifier, sensor unique_ids, and discovery config topics.
//
// The entity key is normally getSourceID(source): the sanitized DisplayName, or
// the sanitized raw ID when no DisplayName is set. Two sources whose names
// sanitize to the same base key would otherwise share device identifiers,
// unique_ids, and config topics and silently overwrite each other's discovery
// (last writer wins). To keep colliding sources distinct while preserving the
// existing unique_ids and HA history in the common case, within a group of
// sources that share a base key exactly one source holds the plain base key and
// every other source is suffixed with "_" + SanitizeID(source.ID). Single-source
// and non-colliding installs therefore keep exactly the keys they had before this
// disambiguation existed.
//
// The assignment is a pure function of the current source list, so it is the
// same after a restart as it was before: within a group, the lexicographically
// smallest raw ID keeps the plain base key and the rest get base+"_"+SanitizeID(ID).
// The price of determinism is that adding a same-name source whose raw ID sorts
// first moves the plain key (and its HA history) to that source; giving sources
// distinct names avoids it. Every returned key is unique, and the result does not
// depend on input slice order.
func SourceEntityKeys(sources []datastore.AudioSource) map[string]string {
	// Group sources by their base entity key.
	groups := make(map[string][]string, len(sources)) // base -> raw IDs
	bases := make([]string, 0, len(sources))
	for _, source := range sources {
		base := getSourceID(source)
		if _, seen := groups[base]; !seen {
			bases = append(bases, base)
		}
		groups[base] = append(groups[base], source.ID)
	}
	slices.Sort(bases)

	keys := make(map[string]string, len(sources))
	used := make(map[string]struct{}, len(sources))
	for _, base := range bases {
		ids := groups[base]
		slices.Sort(ids)
		// Plain base keys are claimed first so a suffixed key can never take a
		// base key another group needs.
		keys[ids[0]] = base
		used[base] = struct{}{}
	}
	for _, base := range bases {
		for _, id := range groups[base][1:] {
			candidate := base + "_" + SanitizeID(id)
			for n := 2; ; n++ {
				if _, taken := used[candidate]; !taken {
					break
				}
				candidate = fmt.Sprintf("%s_%s_%d", base, SanitizeID(id), n)
			}
			keys[id] = candidate
			used[candidate] = struct{}{}
		}
	}
	return keys
}

// SourceEntityKeyCandidates returns every entity key SourceEntityKeys can have
// assigned to source, whatever other sources existed at the time: the plain
// base key, and the key it gets when another same-name source holds the base.
// Removing a source's entities without a record of what was published means
// removing under each candidate that no live source currently owns. (The
// numeric disambiguator of SourceEntityKeys only appears when a suffixed key
// collides with another group's base key and is not covered.)
func SourceEntityKeyCandidates(source datastore.AudioSource) []string {
	base := getSourceID(source)
	return []string{base, base + "_" + SanitizeID(source.ID)}
}

// shortenDisplayName ensures display names stay within maxDisplayNameLength.
// This prevents excessively long entity names in Home Assistant when
// source IDs (like RTSP URLs) are used as fallback display names.
// Uses rune-based operations to safely handle UTF-8 multi-byte characters.
func shortenDisplayName(name string) string {
	runes := []rune(name)
	if len(runes) <= maxDisplayNameLength {
		return name
	}

	// For RTSP-style IDs (rtsp_xxxxx...), keep the prefix and truncate
	const rtspPrefix = "rtsp_"
	const rtspShortenedLength = len(rtspPrefix) + 8 // "rtsp_" + 8 chars of UUID
	if strings.HasPrefix(name, rtspPrefix) && len(runes) > rtspShortenedLength {
		// Example: "rtsp_a1b2c3d4-e5f6..." -> "rtsp_a1b2c3d4"
		return string(runes[:rtspShortenedLength])
	}

	// For URLs or other long strings, truncate intelligently
	// Try to cut at a natural boundary (underscore, hyphen, slash)
	truncated := runes[:maxDisplayNameLength]

	// Find the last natural break point
	lastBreak := -1
	for i := len(truncated) - 1; i >= maxDisplayNameLength/2; i-- {
		if truncated[i] == '_' || truncated[i] == '-' || truncated[i] == '/' {
			lastBreak = i
			break
		}
	}

	if lastBreak > 0 {
		return string(truncated[:lastBreak])
	}

	return string(truncated)
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
	PayloadOn           string           `json:"payload_on,omitempty"`
	PayloadOff          string           `json:"payload_off,omitempty"`
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
// Entity keys come from SourceEntityKeys(sources), so they depend only on the
// current source list.
func (p *Publisher) PublishDiscovery(ctx context.Context, sources []datastore.AudioSource, settings *conf.Settings) error {
	entityKeys := SourceEntityKeys(sources)
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
		if err := p.publishSourceDiscovery(ctx, source, entityKeys[source.ID], settings); err != nil {
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
		Name:           "Status",
		UniqueID:       bridgeID + "_status",
		StateTopic:     StatusTopic(p.config.BaseTopic),
		DeviceClass:    "connectivity",
		EntityCategory: "diagnostic",
		PayloadOn:      StatusPayloadOnline,
		PayloadOff:     StatusPayloadOffline,
		Device: DiscoveryDevice{
			Identifiers:  []string{bridgeID},
			Name:         p.config.DeviceName,
			Manufacturer: branding.Name(),
			Model:        "Bridge",
			SWVersion:    p.config.Version,
		},
		Origin: p.defaultOrigin(),
	}

	topic := p.getBridgeTopic(nodeID)
	return p.publishPayload(ctx, topic, &payload)
}

// publishSourceDiscovery publishes discovery for a specific audio source.
//
// entityKey is the source's entity key from SourceEntityKeys; it keys the device
// identifier, the sensor unique_ids, and the discovery config topics. State
// topics stay keyed by the raw source.ID (which is always unique), so only the
// entity-facing identifiers use entityKey.
func (p *Publisher) publishSourceDiscovery(ctx context.Context, source datastore.AudioSource, entityKey string, settings *conf.Settings) error {
	nodeID := SanitizeID(p.config.NodeID)
	deviceID := fmt.Sprintf("%s_%s_%s", deviceIDPrefix, nodeID, entityKey)
	bridgeID := p.bridgeID(nodeID)

	// Determine display name for the device
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
		Manufacturer: branding.Name(),
		Model:        "Audio Analyzer",
		SWVersion:    p.config.Version,
		ViaDevice:    bridgeID,
	}

	availabilityTopic := StatusTopic(p.config.BaseTopic)

	// Each source's sensors read that source's own state topic, so the
	// templates need no sourceId filter. Filtering on the shared topic cannot
	// work: HA turns a template that renders None into an unknown state, so
	// every other source's detection would clear these sensors (GitHub #4349).
	// The topics are keyed by the raw source.ID, matching the publishers.
	detectionTopic := SourceDetectionTopic(p.config.BaseTopic, source.ID)

	// Publish Last Species sensor
	if err := p.publishSensor(ctx, nodeID, entityKey, SensorSpecies, &DiscoveryPayload{
		Name:              "Last Species",
		UniqueID:          deviceID + "_species",
		StateTopic:        detectionTopic,
		ValueTemplate:     "{{ value_json.CommonName }}",
		Icon:              "mdi:bird",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Confidence sensor
	if err := p.publishSensor(ctx, nodeID, entityKey, SensorConfidence, &DiscoveryPayload{
		Name:              "Confidence",
		UniqueID:          deviceID + "_confidence",
		StateTopic:        detectionTopic,
		ValueTemplate:     "{{ (value_json.Confidence * 100) | round(1) }}",
		UnitOfMeasurement: "%",
		StateClass:        "measurement",
		Icon:              "mdi:percent",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Scientific Name sensor
	if err := p.publishSensor(ctx, nodeID, entityKey, SensorScientificName, &DiscoveryPayload{
		Name:              "Scientific Name",
		UniqueID:          deviceID + "_scientific_name",
		StateTopic:        detectionTopic,
		ValueTemplate:     "{{ value_json.ScientificName }}",
		Icon:              "mdi:format-quote-close",
		AvailabilityTopic: availabilityTopic,
		Device:            device,
	}); err != nil {
		return err
	}

	// Publish Sound Level sensor if sound level monitoring is enabled
	// Band key format: formatBandKey() in internal/audiocore/soundlevel/processor.go produces "1.0_kHz" for 1000 Hz
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := p.publishSensor(ctx, nodeID, entityKey, SensorSoundLevel, &DiscoveryPayload{
			Name:              "Sound Level",
			UniqueID:          deviceID + "_sound_level",
			StateTopic:        SourceSoundLevelTopic(p.config.BaseTopic, source.ID),
			ValueTemplate:     "{{ value_json.b['1.0_kHz'].m }}",
			UnitOfMeasurement: "dB",
			DeviceClass:       "sound_pressure",
			StateClass:        "measurement",
			Icon:              "mdi:volume-high",
			AvailabilityTopic: availabilityTopic,
			Device:            device,
		}); err != nil {
			return err
		}
	} else {
		// Sound level monitoring is off: remove any stale Sound Level sensor by
		// publishing an empty retained payload to its config topic. This makes
		// toggling sound level off take effect immediately in HA. Idempotent when
		// no sensor was ever published (empty retained config is a no-op removal).
		soundLevelConfigTopic := p.getSensorTopic(nodeID, entityKey, SensorSoundLevel)
		if err := p.client.PublishWithRetain(ctx, soundLevelConfigTopic, "", true); err != nil {
			return err
		}
		// Also clear the retained per-source sound level state topic, so the last
		// reading published while monitoring was on does not stay retained on the
		// broker forever after it is turned off.
		soundLevelStateTopic := SourceSoundLevelTopic(p.config.BaseTopic, source.ID)
		if err := p.client.PublishWithRetain(ctx, soundLevelStateTopic, "", true); err != nil {
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
		Name:       branding.Name(),
		SWVersion:  p.config.Version,
		SupportURL: branding.SupportURL(),
	}
}

// bridgeID returns the standardized bridge device identifier.
func (p *Publisher) bridgeID(nodeID string) string {
	return fmt.Sprintf("%s_%s_bridge", deviceIDPrefix, nodeID)
}

// RemoveDiscovery publishes empty retained payloads to remove the bridge and
// every source's sensors and retained per-source state. Entity keys come from
// SourceEntityKeys(sources). It attempts every removal and returns the joined
// error of all per-topic publish failures (nil when all succeeded).
func (p *Publisher) RemoveDiscovery(ctx context.Context, sources []datastore.AudioSource) error {
	entityKeys := SourceEntityKeys(sources)
	log := GetLogger()
	log.Info("Removing Home Assistant discovery messages")

	nodeID := SanitizeID(p.config.NodeID)

	var errs []error

	// Remove bridge
	bridgeTopic := p.getBridgeTopic(nodeID)
	if err := p.client.PublishWithRetain(ctx, bridgeTopic, "", true); err != nil {
		log.Warn("Failed to remove bridge discovery", logger.Error(err))
		errs = append(errs, fmt.Errorf("remove bridge %s: %w", bridgeTopic, err))
	}

	// Remove each source's sensors and retained per-source state under the exact
	// key it was published with.
	for _, source := range sources {
		if err := p.RemoveSourceDiscovery(ctx, source, entityKeys[source.ID]); err != nil {
			log.Warn("Failed to remove source discovery",
				logger.String("source_id", source.ID),
				logger.Error(err))
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// removeSourceConfigTopics empties every sensor discovery config topic for
// entityKey, leaving the per-source state topics untouched. It attempts every
// topic and returns the joined error of the per-topic publish failures.
func (p *Publisher) removeSourceConfigTopics(ctx context.Context, entityKey string) error {
	log := GetLogger()
	nodeID := SanitizeID(p.config.NodeID)
	var errs []error
	for _, sensorType := range AllSensorTypes {
		topic := p.getSensorTopic(nodeID, entityKey, sensorType)
		if err := p.client.PublishWithRetain(ctx, topic, "", true); err != nil {
			log.Warn("Failed to remove sensor discovery",
				logger.String("topic", topic),
				logger.Error(err))
			errs = append(errs, fmt.Errorf("remove config %s: %w", topic, err))
		}
	}
	return errors.Join(errs...)
}

// RemoveSourceConfigs empties only a source's discovery config topics (keyed by
// entityKey) and leaves the per-source state topics intact. It is used when a
// live source's entity key changes: the configs under the old key are orphaned,
// but the state topics (keyed by the unchanged raw source ID) are still being
// published to, so wiping them would drop a live source's retained state. Returns
// the joined error of any per-topic publish failures.
func (p *Publisher) RemoveSourceConfigs(ctx context.Context, entityKey string) error {
	return p.removeSourceConfigTopics(ctx, entityKey)
}

// RemoveSourceDiscovery removes one source's discovery entries and its retained
// per-source state. It first empties every sensor config topic (keyed by
// entityKey), then empties the retained per-source state topics (keyed by the
// raw source.ID), so a retain=true install does not keep orphaned detection or
// sound level payloads for a removed source.
//
// Config topics are cleared before the state topics on purpose: removing a
// config makes Home Assistant drop the entity, so the subsequent empty state
// payload is only ever seen by subscribers still listening on the raw state
// topic (manual subscribers), not by an HA entity that could briefly flap to
// unknown. It attempts every topic and returns the joined error of any per-topic
// publish failures.
//
// entityKey must be the source's key from SourceEntityKeys.
func (p *Publisher) RemoveSourceDiscovery(ctx context.Context, source datastore.AudioSource, entityKey string) error {
	var errs []error

	// 1. Clear the sensor config topics.
	if err := p.removeSourceConfigTopics(ctx, entityKey); err != nil {
		errs = append(errs, err)
	}

	// 2. Clear the retained per-source state topics.
	if err := p.RemoveSourceState(ctx, source); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// RemoveSourceState empties one source's retained per-source state topics
// (detection and sound level, keyed by the raw source.ID) without touching its
// discovery configs. It attempts both topics and returns the joined error.
func (p *Publisher) RemoveSourceState(ctx context.Context, source datastore.AudioSource) error {
	log := GetLogger()
	var errs []error
	for _, topic := range []string{
		SourceDetectionTopic(p.config.BaseTopic, source.ID),
		SourceSoundLevelTopic(p.config.BaseTopic, source.ID),
	} {
		if err := p.client.PublishWithRetain(ctx, topic, "", true); err != nil {
			log.Warn("Failed to remove per-source state",
				logger.String("topic", topic),
				logger.Error(err))
			errs = append(errs, fmt.Errorf("remove state %s: %w", topic, err))
		}
	}
	return errors.Join(errs...)
}
