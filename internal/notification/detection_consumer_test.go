package notification

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/events"
	"go.uber.org/goleak"
)

// TestMain provides goleak verification to detect goroutine leaks
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		goleak.IgnoreTopFunction("github.com/tphakala/birdnet-go/internal/notification.(*Service).cleanupLoop"),
		goleak.IgnoreTopFunction("github.com/tphakala/birdnet-go/internal/notification.(*ResourceEventWorker).cleanupLoop"),
	)
	os.Exit(m.Run())
}

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
	assert.Contains(t, notif.Message, "Turdus migratorius")
	assert.Contains(t, notif.Message, "backyard-camera")
	// Verify confidence percentage is not included to prevent regression
	assert.NotContains(t, notif.Message, "%", "Message should not contain percentage symbol")
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

// TestDetectionNotificationConsumer_PreSanitizedLocations verifies that the notification
// consumer correctly handles pre-sanitized location data from the audio source registry.
// In the new architecture, RTSP URL sanitization happens at the audio source registry level,
// so detection events already contain sanitized display names. The notification layer should
// pass these through unchanged without additional sanitization.
func TestDetectionNotificationConsumer_PreSanitizedLocations(t *testing.T) {
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

	// Test cases for pre-sanitized locations from audio source registry
	// In the new architecture, detection events already contain sanitized display names
	testCases := []struct {
		name             string
		displayLocation  string // Already-sanitized location from registry
		expectedLocation string // Should pass through unchanged
	}{
		{
			name:             "Pre-sanitized RTSP location",
			displayLocation:  "rtsp://192.168.1.100:554/stream1", // Already sanitized by registry
			expectedLocation: "rtsp://192.168.1.100:554/stream1", // Should pass through unchanged
		},
		{
			name:             "Pre-sanitized IPv6 RTSP location",
			displayLocation:  "rtsp://[2001:db8::1]:554/live", // Already sanitized by registry
			expectedLocation: "rtsp://[2001:db8::1]:554/live", // Should pass through unchanged
		},
		{
			name:             "Audio device display name",
			displayLocation:  "USB Audio Device #0", // Display name from registry
			expectedLocation: "USB Audio Device #0", // Should pass through unchanged
		},
		{
			name:             "Custom camera name",
			displayLocation:  "Backyard Camera", // Custom display name from registry
			expectedLocation: "Backyard Camera", // Should pass through unchanged
		},
		{
			name:             "File source display name",
			displayLocation:  "Audio File: recording.wav", // Display name from registry
			expectedLocation: "Audio File: recording.wav", // Should pass through unchanged
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new species detection event with pre-sanitized location
			// This simulates how the real system works: detection events contain
			// already-sanitized display names from the audio source registry
			event, err := events.NewDetectionEvent(
				"Blue Jay",
				"Cyanocitta cristata",
				0.95,
				tc.displayLocation, // Already-sanitized location from registry
				true,               // isNewSpecies
				0,                  // daysSinceFirstSeen
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
			
			// Verify the location passes through unchanged
			assert.Contains(t, notif.Message, tc.expectedLocation)
			
			// Verify the location in metadata passes through unchanged
			assert.Equal(t, tc.expectedLocation, notif.Metadata["location"])
			
			// Verify that credentials never appear (they were removed at registry level)
			assert.NotContains(t, notif.Message, "password")
			assert.NotContains(t, notif.Message, "admin:")
			assert.NotContains(t, notif.Message, "user:")
		})
	}
}
