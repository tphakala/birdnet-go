package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/events"
)

// TestDetectionEventCreation tests the creation of detection events
func TestDetectionEventCreation(t *testing.T) {
	// Test creating a new species detection event
	event := events.NewDetectionEvent(
		"Test Bird",
		"Testus birdus", 
		0.95,
		"test-source",
		true,  // isNewSpecies
		0,     // daysSinceFirstSeen
	)

	assert.NotNil(t, event)
	assert.Equal(t, "Test Bird", event.GetSpeciesName())
	assert.Equal(t, "Testus birdus", event.GetScientificName())
	assert.InDelta(t, 0.95, event.GetConfidence(), 0.001)
	assert.Equal(t, "test-source", event.GetLocation())
	assert.True(t, event.IsNewSpecies())
	assert.Equal(t, 0, event.GetDaysSinceFirstSeen())
	assert.NotNil(t, event.GetTimestamp())
	assert.NotNil(t, event.GetMetadata())
}

// TestSpeciesStatusTracking tests species status tracking functionality
func TestSpeciesStatusTracking(t *testing.T) {
	// Create mock datastore  
	mockDS := &mockDatastore{
		species: []datastore.NewSpeciesData{},
	}

	// Create new species tracker
	tracker := &NewSpeciesTracker{
		speciesFirstSeen: make(map[string]time.Time),
		windowDays:       14,
		ds:               mockDS,
		syncIntervalMins: 60,
	}

	// First detection should be new
	now := time.Now()
	status := tracker.GetSpeciesStatus("Testus birdus", now)
	assert.True(t, status.IsNew)
	assert.Equal(t, 0, status.DaysSinceFirst)

	// Update species
	tracker.UpdateSpecies("Testus birdus", now)

	// Check again - should still be new (same day)
	status = tracker.GetSpeciesStatus("Testus birdus", now)
	assert.True(t, status.IsNew)  // Still within the window
	assert.Equal(t, 0, status.DaysSinceFirst)

	// Check after 5 days - still within window
	future := now.Add(5 * 24 * time.Hour)
	status = tracker.GetSpeciesStatus("Testus birdus", future)
	assert.True(t, status.IsNew)
	assert.Equal(t, 5, status.DaysSinceFirst)

	// Check after 15 days - outside window
	future = now.Add(15 * 24 * time.Hour)
	status = tracker.GetSpeciesStatus("Testus birdus", future)
	assert.False(t, status.IsNew)
	assert.Equal(t, 15, status.DaysSinceFirst)
}

// testDetectionConsumer captures detection events for testing
type testDetectionConsumer struct {
	receivedEvents []events.DetectionEvent
}

func (c *testDetectionConsumer) Name() string {
	return "test-detection-consumer"
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
	c.receivedEvents = append(c.receivedEvents, event)
	return nil
}