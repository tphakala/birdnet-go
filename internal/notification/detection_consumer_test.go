package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/events"
)

func TestDetectionNotificationConsumer(t *testing.T) {
	t.Parallel()

	// Create notification service
	config := &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
	}
	service := NewService(config)
	require.NotNil(t, service)
	defer service.Stop()

	// Create detection consumer
	consumer := NewDetectionNotificationConsumer(service)
	require.NotNil(t, consumer)

	// Test consumer name
	assert.Equal(t, "detection-notification-consumer", consumer.Name())

	// Test that it doesn't support batching
	assert.False(t, consumer.SupportsBatching())

	// Create a new species detection event
	event := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.92,
		"backyard-camera",
		true, // isNewSpecies
		0,    // daysSinceFirstSeen
	)

	// Process the event
	err := consumer.ProcessDetectionEvent(event)
	require.NoError(t, err)

	// Verify notification was created
	notifications, err := service.List(&FilterOptions{
		Types: []Type{TypeDetection},
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, notifications, 1)

	// Verify notification content
	notif := notifications[0]
	assert.Equal(t, TypeDetection, notif.Type)
	assert.Equal(t, PriorityHigh, notif.Priority)
	assert.Contains(t, notif.Title, "New Species Detected: American Robin")
	assert.Contains(t, notif.Message, "First detection of American Robin")
	assert.Contains(t, notif.Message, "92.0% confidence")
	assert.Equal(t, "detection", notif.Component)

	// Verify metadata
	assert.Equal(t, "American Robin", notif.Metadata["species"])
	assert.Equal(t, "Turdus migratorius", notif.Metadata["scientific_name"])
	assert.InDelta(t, 0.92, notif.Metadata["confidence"], 0.001)
	assert.Equal(t, "backyard-camera", notif.Metadata["location"])
	assert.Equal(t, true, notif.Metadata["is_new_species"])
	assert.Equal(t, 0, notif.Metadata["days_since_first_seen"])

	// Test that non-new species don't create notifications
	oldSpeciesEvent := events.NewDetectionEvent(
		"House Sparrow",
		"Passer domesticus",
		0.88,
		"feeder-camera",
		false, // not a new species
		10,    // seen 10 days ago
	)

	err = consumer.ProcessDetectionEvent(oldSpeciesEvent)
	require.NoError(t, err)

	// Should still have only 1 notification
	notifications, err = service.List(&FilterOptions{
		Types: []Type{TypeDetection},
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Len(t, notifications, 1)
}
