package alerting

import (
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

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
		properties[PropertyEventMetadata] = meta
	}

	TryPublish(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  EventDetectionOccurred,
		Properties: properties,
	})

	if event.IsNewSpecies() {
		newSpeciesProps := make(map[string]any, len(properties))
		for k, v := range properties {
			newSpeciesProps[k] = v
		}
		TryPublish(&AlertEvent{
			ObjectType: ObjectTypeDetection,
			EventName:  EventDetectionNewSpecies,
			Properties: newSpeciesProps,
		})
	}

	return nil
}
