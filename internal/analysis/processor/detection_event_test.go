package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/conf"
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
