package alerting

import (
	"maps"

	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var detectionMetadataProperties = []string{
	PropertyDaysSinceLastSeen,
	PropertyNoveltyEpisodeDays,
	PropertyNoveltyEpisodeStart,
	PropertyIsLifer,
}

// DetectionAlertBridge bridges the events.EventBus detection events to the
// alerting event bus. It registers as an events.EventConsumer and publishes
// alert events for each detection.
type DetectionAlertBridge struct {
	log logger.Logger
}

// NewDetectionAlertBridge creates a new bridge consumer.
func NewDetectionAlertBridge(log logger.Logger) *DetectionAlertBridge {
	return &DetectionAlertBridge{log: log}
}

func (b *DetectionAlertBridge) Name() string {
	return "detection-alert-bridge"
}

func (b *DetectionAlertBridge) ProcessEvent(_ events.ErrorEvent) error {
	return nil
}

func (b *DetectionAlertBridge) ProcessBatch(_ []events.ErrorEvent) error {
	return nil
}

func (b *DetectionAlertBridge) SupportsBatching() bool {
	return false
}

// ProcessDetectionEvent publishes detection events to the alert event bus.
// Every detection emits detection.occurred. A detection additionally emits
// detection.new_species (new to this install) or detection.lifer (not on the
// user's life list). A lifer takes precedence: when a detection is both, only
// detection.lifer is emitted, so the user gets a single, more-significant lifer
// alert instead of a duplicate new-species alert with an identical body.
func (b *DetectionAlertBridge) ProcessDetectionEvent(event events.DetectionEvent) error {
	b.log.Debug("Detection event received at alert bridge",
		logger.String("component", "alerting.detection_bridge"),
		logger.String("species", event.GetSpeciesName()),
		logger.String("scientific_name", event.GetScientificName()),
		logger.Float64("confidence", event.GetConfidence()),
		logger.Bool("is_new_species", event.IsNewSpecies()),
		logger.String("operation", "bridge_detection_event"))

	properties := map[string]any{
		PropertySpeciesName:        event.GetSpeciesName(),
		PropertyScientificName:     event.GetScientificName(),
		PropertyConfidence:         event.GetConfidence(),
		PropertyLocation:           event.GetLocation(),
		PropertyEventTimestamp:     event.GetTimestamp(),
		PropertyDaysSinceFirstSeen: event.GetDaysSinceFirstSeen(),
		PropertyIsNewSpecies:       event.IsNewSpecies(),
	}

	meta := event.GetMetadata()
	if len(meta) > 0 {
		properties[PropertyEventMetadata] = maps.Clone(meta)
		for _, propertyName := range detectionMetadataProperties {
			if value, ok := meta[propertyName]; ok {
				properties[propertyName] = value
			}
		}
	}

	isLifer, _ := meta[events.DetectionMetadataIsLifer].(bool)

	// Lifer takes precedence over the per-install "new species" signal: when a
	// detection is both, emit only detection.lifer so the user gets one alert
	// (the more significant one), not a duplicate new-species alert.
	var newSpeciesProps map[string]any
	if event.IsNewSpecies() && !isLifer {
		newSpeciesProps = maps.Clone(properties)
	}

	var liferProps map[string]any
	if isLifer {
		liferProps = maps.Clone(properties)
	}

	TryPublish(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: properties,
		Timestamp:  event.GetTimestamp(),
	})

	if newSpeciesProps != nil {
		TryPublish(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionNewSpecies,
			Properties: newSpeciesProps,
			Timestamp:  event.GetTimestamp(),
		})
	}

	if liferProps != nil {
		TryPublish(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionLifer,
			Properties: liferProps,
			Timestamp:  event.GetTimestamp(),
		})
	}

	return nil
}
