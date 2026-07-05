package alerting

import (
	"maps"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var detectionMetadataProperties = []string{
	PropertyDaysSinceLastSeen,
	PropertyNoveltyEpisodeDays,
	PropertyNoveltyEpisodeStart,
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
// Every detection emits detection.occurred; new species additionally emit
// detection.new_species so rules on either event work as users expect.
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

	if meta := event.GetMetadata(); len(meta) > 0 {
		properties[PropertyEventMetadata] = maps.Clone(meta)
		for _, propertyName := range detectionMetadataProperties {
			if value, ok := meta[propertyName]; ok {
				properties[propertyName] = value
			}
		}
	}

	isInfrequent := isInfrequentDetection(properties)
	properties[PropertyIsInfrequent] = isInfrequent

	var newSpeciesProps map[string]any
	if event.IsNewSpecies() {
		newSpeciesProps = maps.Clone(properties)
	}

	var infrequentProps map[string]any
	if isInfrequent {
		infrequentProps = maps.Clone(properties)
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

	if infrequentProps != nil {
		TryPublish(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionInfrequentSpecies,
			Properties: infrequentProps,
			Timestamp:  event.GetTimestamp(),
		})
	}

	return nil
}

// isInfrequentDetection reports whether a returning detection qualifies as
// "infrequent": tracking enabled and the pre-return absence gap exceeds the
// configured threshold. days_since_last_seen is absent for first-ever and
// same-day detections, so those never qualify. New species detections take
// precedence over the infrequent tier (matching the frontend's lifetime >
// year > season > infrequent ordering), so an already-new detection never
// also qualifies as infrequent.
func isInfrequentDetection(properties map[string]any) bool {
	if isNew, ok := properties[PropertyIsNewSpecies].(bool); ok && isNew {
		return false
	}
	settings := conf.GetSettings()
	if settings == nil {
		return false
	}
	if !settings.Realtime.SpeciesTracking.Enabled {
		return false
	}
	infrequent := settings.Realtime.SpeciesTracking.InfrequentTracking
	if !infrequent.Enabled {
		return false
	}
	days, ok := properties[PropertyDaysSinceLastSeen].(int)
	return ok && days > infrequent.AbsenceDays
}
