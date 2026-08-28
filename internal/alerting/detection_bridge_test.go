package alerting

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

type capturedAlertEvent struct {
	ObjectType string
	EventName  string
	Properties map[string]any
}

func waitForEvents(t *testing.T, mu *sync.Mutex, captured *[]capturedAlertEvent, count int) []capturedAlertEvent {
	t.Helper()
	var result []capturedAlertEvent
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		mu.Lock()
		result = make([]capturedAlertEvent, len(*captured))
		copy(result, *captured)
		mu.Unlock()
		assert.GreaterOrEqual(collect, len(result), count)
	}, 2*time.Second, 20*time.Millisecond)
	return result
}

func setupBridgeWithCapture(t *testing.T) (*DetectionAlertBridge, *sync.Mutex, *[]capturedAlertEvent) {
	t.Helper()

	var mu sync.Mutex
	var captured []capturedAlertEvent

	bus := NewAlertEventBus(nil)
	bus.Subscribe(func(event *AlertEvent) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, capturedAlertEvent{
			ObjectType: event.ObjectType,
			EventName:  event.EventName,
			Properties: event.Properties,
		})
	})
	SetGlobalBus(bus)
	t.Cleanup(func() {
		bus.Stop()
		SetGlobalBus(nil)
	})

	bridge := NewDetectionAlertBridge(
		logger.NewSlogLogger(io.Discard, logger.LogLevelError, nil),
	)

	return bridge, &mu, &captured
}

func TestBridge_OrdinaryDetection_EmitsOccurredOnly(t *testing.T) {
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Test Bird", "Testus birdus", 0.9, "mic", false, 30)
	require.NoError(t, err)

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 1)
	assert.Len(t, result, 1)
	assert.Equal(t, EventDetectionOccurred, result[0].EventName)
	assert.Equal(t, "Test Bird", result[0].Properties[PropertySpeciesName])
	assert.InDelta(t, 0.9, result[0].Properties[PropertyConfidence], 0.001)
	assert.False(t, result[0].Properties[PropertyIsNewSpecies].(bool))
}

func TestBridge_DetectionMetadataPromotedForConditions(t *testing.T) {
	const noveltyEpisodeStart = "2026-05-23T12:00:00Z"

	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Bay-breasted Warbler", "Setophaga castanea", 0.86, "mic", false, 30)
	require.NoError(t, err)
	event.GetMetadata()[PropertyDaysSinceLastSeen] = 12
	event.GetMetadata()[PropertyNoveltyEpisodeDays] = 12
	event.GetMetadata()[PropertyNoveltyEpisodeStart] = noveltyEpisodeStart

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 1)
	require.Len(t, result, 1)
	assert.Equal(t, 12, result[0].Properties[PropertyDaysSinceLastSeen])
	assert.Equal(t, 12, result[0].Properties[PropertyNoveltyEpisodeDays])
	assert.Equal(t, noveltyEpisodeStart, result[0].Properties[PropertyNoveltyEpisodeStart])
}

func TestBridge_NewSpecies_EmitsBothEvents(t *testing.T) {
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Test Bird", "Testus birdus", 0.9, "mic", true, 0)
	require.NoError(t, err)

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 2)
	assert.Len(t, result, 2)

	eventNames := []string{result[0].EventName, result[1].EventName}
	assert.Contains(t, eventNames, EventDetectionOccurred)
	assert.Contains(t, eventNames, EventDetectionNewSpecies)

	// Verify both events carry correct properties
	for _, r := range result {
		assert.Equal(t, "Test Bird", r.Properties[PropertySpeciesName])
		assert.Equal(t, "Testus birdus", r.Properties[PropertyScientificName])
		assert.InDelta(t, 0.9, r.Properties[PropertyConfidence], 0.001)
		assert.True(t, r.Properties[PropertyIsNewSpecies].(bool))
	}
}

// setInfrequentTracking configures the global settings snapshot for infrequent
// tracking and restores it after the test.
func setInfrequentTracking(t *testing.T, enabled bool, absenceDays int) {
	t.Helper()
	settings := conftest.GetTestSettings()
	settings.Realtime.SpeciesTracking.Enabled = true
	settings.Realtime.SpeciesTracking.InfrequentTracking = conf.InfrequentTrackingSettings{
		Enabled:     enabled,
		AbsenceDays: absenceDays,
	}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
}

func TestBridge_InfrequentSpecies_EmitsInfrequentEvent(t *testing.T) {
	setInfrequentTracking(t, true, 14)
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Bay-breasted Warbler", "Setophaga castanea", 0.86, "mic", false, 30)
	require.NoError(t, err)
	event.GetMetadata()[PropertyDaysSinceLastSeen] = 20 // > threshold

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 2)
	require.Len(t, result, 2)

	eventNames := []string{result[0].EventName, result[1].EventName}
	assert.Contains(t, eventNames, EventDetectionOccurred)
	assert.Contains(t, eventNames, EventDetectionInfrequentSpecies)
	for _, r := range result {
		assert.Equal(t, true, r.Properties[PropertyIsInfrequent])
	}
}

func TestBridge_InfrequentBelowThreshold_NoInfrequentEvent(t *testing.T) {
	setInfrequentTracking(t, true, 14)
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Bay-breasted Warbler", "Setophaga castanea", 0.86, "mic", false, 30)
	require.NoError(t, err)
	event.GetMetadata()[PropertyDaysSinceLastSeen] = 10 // <= threshold

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 1)
	assert.Len(t, result, 1)
	assert.Equal(t, EventDetectionOccurred, result[0].EventName)
	assert.Equal(t, false, result[0].Properties[PropertyIsInfrequent])
}

func TestBridge_InfrequentDisabled_NoInfrequentEvent(t *testing.T) {
	setInfrequentTracking(t, false, 14)
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Bay-breasted Warbler", "Setophaga castanea", 0.86, "mic", false, 30)
	require.NoError(t, err)
	event.GetMetadata()[PropertyDaysSinceLastSeen] = 20 // > threshold but tracking disabled

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 1)
	assert.Len(t, result, 1)
	assert.Equal(t, EventDetectionOccurred, result[0].EventName)
	assert.Equal(t, false, result[0].Properties[PropertyIsInfrequent])
}

func TestBridge_NewSpeciesTakesPrecedenceOverInfrequent_NoDuplicateEvent(t *testing.T) {
	setInfrequentTracking(t, true, 14)
	bridge, mu, captured := setupBridgeWithCapture(t)

	// A new species whose absence gap also exceeds the infrequent threshold
	// must only emit the new-species event, never an additional infrequent one.
	event, err := events.NewDetectionEvent("Test Bird", "Testus birdus", 0.9, "mic", true, 0)
	require.NoError(t, err)
	event.GetMetadata()[PropertyDaysSinceLastSeen] = 20 // > threshold

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 2)
	assert.Len(t, result, 2)

	eventNames := []string{result[0].EventName, result[1].EventName}
	assert.Contains(t, eventNames, EventDetectionOccurred)
	assert.Contains(t, eventNames, EventDetectionNewSpecies)
	assert.NotContains(t, eventNames, EventDetectionInfrequentSpecies)

	for _, r := range result {
		assert.Equal(t, false, r.Properties[PropertyIsInfrequent])
	}
}

func TestBridge_NewSpecies_IndependentPropertyMaps(t *testing.T) {
	bridge, mu, captured := setupBridgeWithCapture(t)

	event, err := events.NewDetectionEvent("Test Bird", "Testus birdus", 0.9, "mic", true, 0)
	require.NoError(t, err)

	require.NoError(t, bridge.ProcessDetectionEvent(event))

	result := waitForEvents(t, mu, captured, 2)
	require.Len(t, result, 2)

	// Mutating one event's properties must not affect the other
	result[0].Properties["test_marker"] = "mutated"
	_, hasMarker := result[1].Properties["test_marker"]
	assert.False(t, hasMarker, "property maps should be independent copies")
}
