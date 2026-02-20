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

	TryPublish(&AlertEvent{
		ObjectType: ObjectTypeDetection,
		EventName:  eventName,
		Properties: map[string]any{
			PropertySpeciesName:    event.GetSpeciesName(),
			PropertyScientificName: event.GetScientificName(),
			PropertyConfidence:     event.GetConfidence(),
			PropertyLocation:       event.GetLocation(),
		},
	})

	return nil
}
