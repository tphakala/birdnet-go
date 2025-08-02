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
	event, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.92,
		"backyard-camera",
		true, // isNewSpecies
		0,    // daysSinceFirstSeen
	)
	require.NoError(t, err)

	// Process the event
	err = consumer.ProcessDetectionEvent(event)
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
	oldSpeciesEvent, err := events.NewDetectionEvent(
		"House Sparrow",
		"Passer domesticus",
		0.88,
		"feeder-camera",
		false, // not a new species
		10,    // seen 10 days ago
	)
	require.NoError(t, err)

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

func TestDetectionNotificationConsumer_RTSPSanitization(t *testing.T) {
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

	// Test cases for RTSP URL sanitization
	testCases := []struct {
		name             string
		rtspURL          string
		expectedLocation string
	}{
		{
			name:             "RTSP URL with credentials",
			rtspURL:          "rtsp://admin:password123@192.168.1.100:554/stream1",
			expectedLocation: "rtsp://192.168.1.100:554/stream1", // Path is now preserved for debugging
		},
		{
			name:             "RTSP URL without credentials",
			rtspURL:          "rtsp://192.168.1.100:554/stream1",
			expectedLocation: "rtsp://192.168.1.100:554/stream1", // Path is now preserved for debugging
		},
		{
			name:             "RTSP URL with IPv6 and credentials",
			rtspURL:          "rtsp://user:pass@[2001:db8::1]:554/live",
			expectedLocation: "rtsp://[2001:db8::1]:554/live", // Path is now preserved for debugging
		},
		{
			name:             "Non-RTSP URL (should remain unchanged)",
			rtspURL:          "backyard-camera",
			expectedLocation: "backyard-camera",
		},
		{
			name:             "HTTP URL (should remain unchanged)",
			rtspURL:          "http://example.com/stream",
			expectedLocation: "http://example.com/stream",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new species detection event with RTSP URL
			event, err := events.NewDetectionEvent(
				"Blue Jay",
				"Cyanocitta cristata",
				0.95,
				tc.rtspURL,
				true, // isNewSpecies
				0,    // daysSinceFirstSeen
			)
			require.NoError(t, err)

			// Process the event
			err = consumer.ProcessDetectionEvent(event)
			require.NoError(t, err)

			// Get the latest notification
			notifications, err := service.List(&FilterOptions{
				Types: []Type{TypeDetection},
				Limit: 1,
			})
			require.NoError(t, err)
			require.Len(t, notifications, 1)

			notif := notifications[0]
			
			// Verify the location in message is sanitized
			assert.Contains(t, notif.Message, tc.expectedLocation)
			assert.NotContains(t, notif.Message, "password123")
			assert.NotContains(t, notif.Message, "admin:")
			assert.NotContains(t, notif.Message, "user:pass")
			
			// Verify the location in metadata is sanitized
			assert.Equal(t, tc.expectedLocation, notif.Metadata["location"])
		})
	}
}
