package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/events"
)

// Test constants for better readability and maintainability
const (
	testSpeciesCommonName     = "Test Bird"
	testSpeciesScientificName = "Testus birdus"
	testConfidence            = 0.95
	testConfidenceDelta       = 0.001
	testSource                = "test-source"
	testWindowDays            = 14
	testSyncIntervalMins      = 60
	testCacheTTL              = 30 * time.Second
	testSeasonCacheTTL        = time.Hour
	testDaysWithinWindow      = 5
	testDaysOutsideWindow     = 15
	testConsumerName          = "test-detection-consumer"
	testSuppressionHours      = 168
)

// TestDetectionEventCreation tests the creation of detection events
func TestDetectionEventCreation(t *testing.T) {
	t.Parallel()

	// Test creating a new species detection event
	event, err := events.NewDetectionEvent(
		testSpeciesCommonName,
		testSpeciesScientificName,
		testConfidence,
		testSource,
		true, // isNewSpecies
		0,    // daysSinceFirstSeen
	)
	require.NoError(t, err)

	assert.NotNil(t, event)
	assert.Equal(t, testSpeciesCommonName, event.GetSpeciesName())
	assert.Equal(t, testSpeciesScientificName, event.GetScientificName())
	assert.InDelta(t, testConfidence, event.GetConfidence(), testConfidenceDelta)
	assert.Equal(t, testSource, event.GetLocation())
	assert.True(t, event.IsNewSpecies())
	assert.Equal(t, 0, event.GetDaysSinceFirstSeen())
	assert.NotNil(t, event.GetTimestamp())
	assert.NotNil(t, event.GetMetadata())
}

// TestSpeciesStatusTracking tests species status tracking functionality
func TestSpeciesStatusTracking(t *testing.T) {
	t.Parallel()

	// Create mock datastore using generated mocks (BG-21)
	mockDS := mocks.NewMockInterface(t)
	// Note: This test doesn't call InitFromDatabase, so no expectations needed

	// First detection should be new
	now := time.Now()

	// Create new species tracker using constructor with minimal test configuration
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: testWindowDays,
		SyncIntervalMinutes:  testSyncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := species.NewTrackerFromSettings(mockDS, settings)
	status := tracker.GetSpeciesStatus(testSpeciesScientificName, now)
	assert.True(t, status.IsNew)
	assert.Equal(t, 0, status.DaysSinceFirst)

	// Update species
	tracker.UpdateSpecies(testSpeciesScientificName, now)

	// Check again - should still be new (same day)
	status = tracker.GetSpeciesStatus(testSpeciesScientificName, now)
	assert.True(t, status.IsNew) // Still within the window
	assert.Equal(t, 0, status.DaysSinceFirst)

	// Check after testDaysWithinWindow days - still within window
	future := now.Add(testDaysWithinWindow * 24 * time.Hour)
	status = tracker.GetSpeciesStatus(testSpeciesScientificName, future)
	assert.True(t, status.IsNew)
	assert.Equal(t, testDaysWithinWindow, status.DaysSinceFirst)

	// Check after testDaysOutsideWindow days - outside window
	future = now.Add(testDaysOutsideWindow * 24 * time.Hour)
	status = tracker.GetSpeciesStatus(testSpeciesScientificName, future)
	assert.False(t, status.IsNew)
	assert.Equal(t, testDaysOutsideWindow, status.DaysSinceFirst)

	// No expectations to verify since this test doesn't call InitFromDatabase
}

// testDetectionConsumer is a test helper that captures detection events for testing.
// It implements the detection event consumer interface and provides thread-safe
// access to received events for verification in concurrent test scenarios.
type testDetectionConsumer struct {
	mu             sync.Mutex
	receivedEvents []events.DetectionEvent
}

func (c *testDetectionConsumer) Name() string {
	return testConsumerName
}

func (c *testDetectionConsumer) ProcessEvent(event events.ErrorEvent) error {
	return nil
}

func (c *testDetectionConsumer) ProcessBatch(errorEvents []events.ErrorEvent) error {
	return nil
}

func (c *testDetectionConsumer) SupportsBatching() bool {
	return false
}

func (c *testDetectionConsumer) ProcessDetectionEvent(event events.DetectionEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.receivedEvents = append(c.receivedEvents, event)
	return nil
}

// GetReceivedEvents returns a copy of the received events slice for safe concurrent access
func (c *testDetectionConsumer) GetReceivedEvents() []events.DetectionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Return a copy to prevent race conditions
	eventsCopy := make([]events.DetectionEvent, len(c.receivedEvents))
	copy(eventsCopy, c.receivedEvents)
	return eventsCopy
}

func setupEventBusWithConsumer(t *testing.T) *testDetectionConsumer {
	t.Helper()
	events.ResetForTesting()
	t.Cleanup(events.ResetForTesting)
	eb, err := events.Initialize(&events.Config{BufferSize: 100, Workers: 1, Enabled: true})
	require.NoError(t, err)
	c := &testDetectionConsumer{}
	require.NoError(t, eb.RegisterConsumer(c))
	return c
}

// TestPublishDetectionEvent_OrdinaryDetection verifies that non-new-species
// detections reach the event bus with isNewSpecies=false.
func TestPublishDetectionEvent_OrdinaryDetection(t *testing.T) {
	consumer := setupEventBusWithConsumer(t)

	det := testDetection()
	action := &DatabaseAction{
		Settings:      &conf.Settings{Debug: true},
		Result:        det.Result,
		CorrelationID: "test-ordinary-det",
	}

	action.publishDetectionEvent(false, 30, species.NoveltyStatus{})

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		received := consumer.GetReceivedEvents()
		assert.Len(collect, received, 1)
	}, 2*time.Second, 50*time.Millisecond)

	received := consumer.GetReceivedEvents()
	assert.Equal(t, det.Result.Species.CommonName, received[0].GetSpeciesName())
	assert.Equal(t, det.Result.Species.ScientificName, received[0].GetScientificName())
	assert.False(t, received[0].IsNewSpecies())
	metadata := received[0].GetMetadata()
	assert.NotContains(t, metadata, events.DetectionMetadataDaysSinceLastSeen)
	assert.NotContains(t, metadata, events.DetectionMetadataNoveltyEpisodeDays)
}

func TestPublishDetectionEvent_InactiveNoveltyOmitsEpisodeSentinel(t *testing.T) {
	const inactiveNoveltyEpisodeDays = -1

	consumer := setupEventBusWithConsumer(t)

	det := testDetection()
	action := &DatabaseAction{
		Settings:      &conf.Settings{Debug: true},
		Result:        det.Result,
		CorrelationID: "test-inactive-novelty",
	}

	action.publishDetectionEvent(false, 30, species.NoveltyStatus{
		DaysSinceLastSeen:    0,
		NoveltyEpisodeDays:   inactiveNoveltyEpisodeDays,
		NoveltyEpisodeActive: false,
	})

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		received := consumer.GetReceivedEvents()
		assert.Len(collect, received, 1)
	}, 2*time.Second, 50*time.Millisecond)

	metadata := consumer.GetReceivedEvents()[0].GetMetadata()
	assert.NotContains(t, metadata, events.DetectionMetadataDaysSinceLastSeen)
	assert.NotContains(t, metadata, events.DetectionMetadataNoveltyEpisodeDays)
	assert.NotContains(t, metadata, events.DetectionMetadataNoveltyEpisodeStart)
}

func TestPublishDetectionEvent_ActiveNoveltyIncludesSameDayLastSeen(t *testing.T) {
	consumer := setupEventBusWithConsumer(t)

	det := testDetection()
	action := &DatabaseAction{
		Settings:      &conf.Settings{Debug: true},
		Result:        det.Result,
		CorrelationID: "test-active-novelty-same-day",
	}

	episodeStart := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	action.publishDetectionEvent(false, 30, species.NoveltyStatus{
		DaysSinceLastSeen:    0,
		NoveltyEpisodeDays:   12,
		NoveltyEpisodeStart:  episodeStart,
		NoveltyEpisodeActive: true,
	})

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		received := consumer.GetReceivedEvents()
		assert.Len(collect, received, 1)
	}, 2*time.Second, 50*time.Millisecond)

	metadata := consumer.GetReceivedEvents()[0].GetMetadata()
	assert.Equal(t, 0, metadata[events.DetectionMetadataDaysSinceLastSeen])
	assert.Equal(t, 12, metadata[events.DetectionMetadataNoveltyEpisodeDays])
	assert.Equal(t, episodeStart.Format(time.RFC3339), metadata[events.DetectionMetadataNoveltyEpisodeStart])
}

// TestPublishDetectionEvent_NewSpecies verifies that new-species detections
// still go through suppression and reach the event bus with isNewSpecies=true.
func TestPublishDetectionEvent_NewSpecies(t *testing.T) {
	consumer := setupEventBusWithConsumer(t)

	det := testDetection()
	action := &DatabaseAction{
		Settings:      &conf.Settings{Debug: true},
		Result:        det.Result,
		CorrelationID: "test-new-species-det",
	}

	action.publishDetectionEvent(true, 0, species.NoveltyStatus{})

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		received := consumer.GetReceivedEvents()
		assert.Len(collect, received, 1)
	}, 2*time.Second, 50*time.Millisecond)

	received := consumer.GetReceivedEvents()
	assert.Equal(t, det.Result.Species.CommonName, received[0].GetSpeciesName())
	assert.True(t, received[0].IsNewSpecies())
}

// TestPublishDetectionEvent_SuppressedNewSpecies verifies that a suppressed
// new species detection still reaches the event bus as an ordinary detection
// (isNewSpecies=false) so detection.occurred alert rules still fire.
func TestPublishDetectionEvent_SuppressedNewSpecies(t *testing.T) {
	consumer := setupEventBusWithConsumer(t)

	mockDS := mocks.NewMockInterface(t)
	mockDS.EXPECT().
		GetActiveNotificationHistory(mock.Anything, mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).
		Maybe()
	mockDS.EXPECT().
		SaveNotificationHistory(mock.Anything, mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).
		Maybe()

	tracker := species.NewTrackerFromSettings(mockDS, &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         testWindowDays,
		SyncIntervalMinutes:          testSyncIntervalMins,
		NotificationSuppressionHours: testSuppressionHours,
	})

	det := testDetection()
	// Record a notification first so next call is suppressed
	tracker.RecordNotificationSent(det.Result.Species.ScientificName, det.Result.BeginTime)

	action := &DatabaseAction{
		Settings:          &conf.Settings{Debug: true},
		Result:            det.Result,
		CorrelationID:     "test-suppressed",
		NewSpeciesTracker: tracker,
	}

	action.publishDetectionEvent(true, 0, species.NoveltyStatus{})

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		received := consumer.GetReceivedEvents()
		assert.Len(collect, received, 1)
	}, 2*time.Second, 50*time.Millisecond)

	received := consumer.GetReceivedEvents()
	assert.Equal(t, det.Result.Species.CommonName, received[0].GetSpeciesName())
	assert.False(t, received[0].IsNewSpecies(), "suppressed new species should publish as ordinary detection")
}

// TestPublishDetectionEvent_NoEventBus verifies graceful handling when the
// event bus is not initialized.
func TestPublishDetectionEvent_NoEventBus(t *testing.T) {
	events.ResetForTesting()
	t.Cleanup(events.ResetForTesting)

	det := testDetection()
	action := &DatabaseAction{
		Settings:      &conf.Settings{Debug: true},
		Result:        det.Result,
		CorrelationID: "test-no-bus",
	}

	// Should not panic when event bus is not initialized
	assert.NotPanics(t, func() {
		action.publishDetectionEvent(false, 10, species.NoveltyStatus{})
		action.publishDetectionEvent(true, 0, species.NoveltyStatus{})
	})
}
