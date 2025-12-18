package notification

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
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

// setupTestServiceAndConsumer creates a notification service and consumer for testing.
// Returns the service, consumer, and a cleanup function that should be called with defer.
func setupTestServiceAndConsumer(t *testing.T) (*Service, *DetectionNotificationConsumer, func()) {
	t.Helper()
	config := &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
	}
	service := NewService(config)
	require.NotNil(t, service)
	consumer := NewDetectionNotificationConsumer(service)
	cleanup := func() {
		service.Stop()
	}
	return service, consumer, cleanup
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
	// Default fallback title format when settings are nil: "New Species Detected: %s"
	assert.Contains(t, notif.Title, "New Species Detected: American Robin")
	// Default fallback message format when settings are nil: "First detection of %s (%s) at %s"
	assert.Contains(t, notif.Message, "First detection of American Robin")
	assert.Contains(t, notif.Message, "Turdus migratorius")
	assert.Contains(t, notif.Message, "backyard-camera")
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

			// Default fallback message format when settings are nil: "First detection of %s (%s) at %s"
			assert.Contains(t, notif.Message, "First detection of Blue Jay")
			assert.Contains(t, notif.Message, "Cyanocitta cristata")
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

// TestDetectionNotificationConsumer_MetadataFieldsExposure verifies that all TemplateData fields
// are exposed in notification metadata with the bg_ prefix for use in provider templates.
// See: https://github.com/tphakala/birdnet-go/issues/1457
func TestDetectionNotificationConsumer_MetadataFieldsExposure(t *testing.T) {
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

	// Create a new species detection event with GPS coordinates
	event, err := events.NewDetectionEvent(
		"Northern Cardinal",
		"Cardinalis cardinalis", //nolint:misspell // Cardinalis is a scientific name, not a misspelling
		0.95,
		"backyard-camera",
		true, // isNewSpecies
		5,    // daysSinceFirstSeen
	)
	require.NoError(t, err)

	// Add obviously fake GPS coordinates to metadata for testing
	metadata := event.GetMetadata()
	metadata["latitude"] = 0.000001  // Test value - not a real location
	metadata["longitude"] = 0.000001 // Test value - not a real location

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

	notif := notifications[0]

	// Verify all bg_ prefixed metadata fields are present
	assert.NotEmpty(t, notif.Metadata["bg_detection_url"], "bg_detection_url should be present")
	assert.Contains(t, notif.Metadata["bg_detection_url"], "/ui/detections", "detection URL should contain UI path")

	assert.NotEmpty(t, notif.Metadata["bg_image_url"], "bg_image_url should be present")
	assert.Contains(t, notif.Metadata["bg_image_url"], "Cardinalis", "image URL should contain scientific name") //nolint:misspell // Cardinalis is a scientific name

	assert.Equal(t, "95", notif.Metadata["bg_confidence_percent"], "bg_confidence_percent should be 95")

	assert.NotEmpty(t, notif.Metadata["bg_detection_time"], "bg_detection_time should be present")
	assert.NotEmpty(t, notif.Metadata["bg_detection_date"], "bg_detection_date should be present")

	// Verify GPS coordinates are exposed
	assert.InDelta(t, 0.000001, notif.Metadata["bg_latitude"], 0.000001, "bg_latitude should match input")
	assert.InDelta(t, 0.000001, notif.Metadata["bg_longitude"], 0.000001, "bg_longitude should match input")

	// Verify original metadata fields are still present (backward compatibility)
	assert.Equal(t, "Northern Cardinal", notif.Metadata["species"])
	assert.Equal(t, "Cardinalis cardinalis", notif.Metadata["scientific_name"]) //nolint:misspell // Cardinalis is a scientific name
	assert.InDelta(t, 0.95, notif.Metadata["confidence"], 0.001)
	assert.Equal(t, "backyard-camera", notif.Metadata["location"])
	assert.Equal(t, true, notif.Metadata["is_new_species"])
	assert.Equal(t, 5, notif.Metadata["days_since_first_seen"])
}

// TestDetectionNotificationConsumer_MetadataFieldsWithoutGPS verifies that GPS fields
// are present but set to 0 when no GPS coordinates are provided.
func TestDetectionNotificationConsumer_MetadataFieldsWithoutGPS(t *testing.T) {
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

	// Create a new species detection event WITHOUT GPS coordinates
	event, err := events.NewDetectionEvent(
		"Blue Jay",
		"Cyanocitta cristata",
		0.88,
		"feeder-camera",
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

	notif := notifications[0]

	// Verify GPS fields are present but set to 0
	assert.InDelta(t, 0.0, notif.Metadata["bg_latitude"], 0.000001, "bg_latitude should be 0 when not configured")
	assert.InDelta(t, 0.0, notif.Metadata["bg_longitude"], 0.000001, "bg_longitude should be 0 when not configured")

	// Verify other bg_ fields are still present
	assert.NotEmpty(t, notif.Metadata["bg_detection_url"])
	assert.NotEmpty(t, notif.Metadata["bg_image_url"])
	assert.Equal(t, "88", notif.Metadata["bg_confidence_percent"])
}

// TestDetectionNotificationConsumer_ConfidenceThreshold verifies that detections
// below the configured confidence threshold are filtered out.
//
//nolint:dupl // Test structure intentionally similar to cooldown test for readability
func TestDetectionNotificationConsumer_ConfidenceThreshold(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests share global settingsInstance

	// Set up test settings with confidence threshold
	settings := conf.GetTestSettings()
	settings.Notification.Push.MinConfidenceThreshold = 0.80 // 80% threshold
	conf.SetTestSettings(settings)
	defer conf.SetTestSettings(nil) // Clean up

	service, consumer, cleanup := setupTestServiceAndConsumer(t)
	defer cleanup()

	// Test: Detection ABOVE threshold should create notification
	highConfEvent, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.92, // Above 80% threshold
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(highConfEvent)
	require.NoError(t, err)

	notifications, err := service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1, "High confidence detection should create notification")

	// Test: Detection BELOW threshold should NOT create notification
	lowConfEvent, err := events.NewDetectionEvent(
		"Blue Jay",
		"Cyanocitta cristata",
		0.65, // Below 80% threshold
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(lowConfEvent)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1, "Low confidence detection should NOT create notification")

	// Test: Detection AT threshold should create notification
	atThresholdEvent, err := events.NewDetectionEvent(
		"Northern Cardinal",
		"Cardinalis cardinalis", //nolint:misspell // Scientific name, not a misspelling
		0.80, // Exactly at threshold
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(atThresholdEvent)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 2, "Detection at threshold should create notification")
}

// TestDetectionNotificationConsumer_SpeciesCooldown verifies that the same species
// doesn't trigger multiple notifications within the cooldown period.
//
//nolint:dupl // Test structure intentionally similar to confidence test for readability
func TestDetectionNotificationConsumer_SpeciesCooldown(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests share global settingsInstance

	// Set up test settings with cooldown
	settings := conf.GetTestSettings()
	settings.Notification.Push.SpeciesCooldownMinutes = 60 // 60 minute cooldown
	conf.SetTestSettings(settings)
	defer conf.SetTestSettings(nil) // Clean up

	service, consumer, cleanup := setupTestServiceAndConsumer(t)
	defer cleanup()

	// First detection should create notification
	event1, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.92,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(event1)
	require.NoError(t, err)

	notifications, err := service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1, "First detection should create notification")

	// Second detection of SAME species should be blocked by cooldown
	event2, err := events.NewDetectionEvent(
		"American Robin", // Same species
		"Turdus migratorius",
		0.95,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(event2)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1, "Second detection of same species should be blocked by cooldown")

	// Detection of DIFFERENT species should create notification
	event3, err := events.NewDetectionEvent(
		"Blue Jay", // Different species
		"Cyanocitta cristata",
		0.88,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(event3)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 2, "Different species should create notification")
}

// TestDetectionNotificationConsumer_CooldownExpiration verifies that cooldown expires correctly.
func TestDetectionNotificationConsumer_CooldownExpiration(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests share global settingsInstance

	// Set up test settings with very short cooldown for testing
	settings := conf.GetTestSettings()
	settings.Notification.Push.SpeciesCooldownMinutes = 1 // 1 minute cooldown
	conf.SetTestSettings(settings)
	defer conf.SetTestSettings(nil) // Clean up

	service, consumer, cleanup := setupTestServiceAndConsumer(t)
	defer cleanup()

	// First detection
	event1, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.92,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(event1)
	require.NoError(t, err)

	notifications, err := service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1)

	// Manually expire the cooldown by manipulating the internal map
	// This simulates time passing without actually waiting
	consumer.cooldownMu.Lock()
	consumer.speciesCooldowns["American Robin"] = time.Now().Add(-2 * time.Minute) // Set to 2 minutes ago
	consumer.cooldownMu.Unlock()

	// Second detection should now succeed since cooldown expired
	event2, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.95,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(event2)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 2, "Detection after cooldown expired should create notification")
}

// TestDetectionNotificationConsumer_DisabledFiltering verifies that filtering
// is disabled when threshold/cooldown are set to 0.
func TestDetectionNotificationConsumer_DisabledFiltering(t *testing.T) {
	// Note: Cannot use t.Parallel() because tests share global settingsInstance

	// Set up test settings with filtering disabled (0 values)
	settings := conf.GetTestSettings()
	settings.Notification.Push.MinConfidenceThreshold = 0 // Disabled
	settings.Notification.Push.SpeciesCooldownMinutes = 0 // Disabled
	conf.SetTestSettings(settings)
	defer conf.SetTestSettings(nil) // Clean up

	service, consumer, cleanup := setupTestServiceAndConsumer(t)
	defer cleanup()

	// Low confidence detection should create notification when threshold is 0
	lowConfEvent, err := events.NewDetectionEvent(
		"American Robin",
		"Turdus migratorius",
		0.15, // Very low confidence
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(lowConfEvent)
	require.NoError(t, err)

	notifications, err := service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 1, "Low confidence should pass when threshold is 0")

	// Same species should create another notification when cooldown is 0
	secondEvent, err := events.NewDetectionEvent(
		"American Robin", // Same species
		"Turdus migratorius",
		0.20,
		"backyard-camera",
		true,
		0,
	)
	require.NoError(t, err)

	err = consumer.ProcessDetectionEvent(secondEvent)
	require.NoError(t, err)

	notifications, err = service.List(&FilterOptions{Types: []Type{TypeDetection}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, notifications, 2, "Same species should pass when cooldown is 0")
}
