// Package notification provides a system for managing and broadcasting notifications
// throughout the BirdNET-Go application.
package notification

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"log/slog"
)

// DetectionNotificationConsumer handles detection events and creates notifications for new species
type DetectionNotificationConsumer struct {
	service *Service
	logger  *slog.Logger
}

// NewDetectionNotificationConsumer creates a new consumer for detection events
func NewDetectionNotificationConsumer(service *Service) *DetectionNotificationConsumer {
	return &DetectionNotificationConsumer{
		service: service,
		logger:  service.logger,
	}
}

// Name returns the consumer name for identification
func (c *DetectionNotificationConsumer) Name() string {
	return "detection-notification-consumer"
}

// ProcessEvent implements the EventConsumer interface (not used for detection events)
func (c *DetectionNotificationConsumer) ProcessEvent(event events.ErrorEvent) error {
	// This consumer only handles detection events through ProcessDetectionEvent
	return nil
}

// ProcessBatch implements the EventConsumer interface (not used)
func (c *DetectionNotificationConsumer) ProcessBatch(errorEvents []events.ErrorEvent) error {
	// Batch processing not implemented for detection events
	return nil
}

// SupportsBatching indicates whether this consumer supports batch processing
func (c *DetectionNotificationConsumer) SupportsBatching() bool {
	return false
}

// ProcessDetectionEvent processes a single detection event
func (c *DetectionNotificationConsumer) ProcessDetectionEvent(event events.DetectionEvent) error {
	// Only process new species detections
	if !event.IsNewSpecies() {
		return nil
	}

	// Sanitize location to remove RTSP credentials
	sanitizedLocation := privacy.SanitizeRTSPUrl(event.GetLocation())

	// Create notification for new species
	title := fmt.Sprintf("New Species Detected: %s", event.GetSpeciesName())
	message := fmt.Sprintf(
		"First detection of %s (%s) at %s",
		event.GetSpeciesName(),
		event.GetScientificName(),
		sanitizedLocation,
	)

	notification := NewNotification(TypeDetection, PriorityHigh, title, message).
		WithComponent("detection").
		WithMetadata("species", event.GetSpeciesName()).
		WithMetadata("scientific_name", event.GetScientificName()).
		WithMetadata("confidence", event.GetConfidence()).
		WithMetadata("location", sanitizedLocation).
		WithMetadata("is_new_species", true).
		WithMetadata("days_since_first_seen", event.GetDaysSinceFirstSeen()).
		WithExpiry(24 * time.Hour) // New species notifications expire after 24 hours

	// Add the notification through the service
	// First save to store
	if err := c.service.store.Save(notification); err != nil {
		c.logger.Error("failed to save new species notification",
			"species", event.GetSpeciesName(),
			"error", err,
		)
		return fmt.Errorf("failed to save notification: %w", err)
	}
	
	// Then broadcast to subscribers
	c.service.broadcast(notification)

	c.logger.Info("created new species notification",
		"species", event.GetSpeciesName(),
		"confidence", event.GetConfidence(),
		"location", sanitizedLocation,
	)

	return nil
}