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
func (b *DetectionAlertBridge) ProcessDetectionEvent(event events.DetectionEvent) error {
	eventName := EventDetectionOccurred
	if event.IsNewSpecies() {
		eventName = EventDetectionNewSpecies
	}

	properties := map[string]any{
		PropertySpeciesName:    event.GetSpeciesName(),
		PropertyScientificName: event.GetScientificName(),
		PropertyConfidence:     event.GetConfidence(),
		PropertyLocation:       event.GetLocation(),
		// Additional fields for notification metadata enrichment.
		// These are not used for condition evaluation but are passed through
		// to the notification adapter so webhook templates can reference them.
		PropertyEventTimestamp:     event.GetTimestamp(),
		PropertyDaysSinceFirstSeen: event.GetDaysSinceFirstSeen(),
		PropertyIsNewSpecies:       event.IsNewSpecies(),
	}

	// Pass through raw event metadata (note_id, latitude, longitude, image_url, begin_time)
	// so the notification adapter can build full template data.
	if meta := event.GetMetadata(); len(meta) > 0 {
		properties[PropertyEventMetadata] = meta
	}

	TryPublish(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  eventName,
		Properties: properties,
	})

	return nil
}
