// eventtracker.go
package processor

import (
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// EventType represents the types of events to be tracked.
type EventType int

const (
	DatabaseSave      EventType = iota // Represents a database save event
	LogToFile                          // Represents a log to file event
	SendNotification                   // Represents a send notification event
	BirdWeatherSubmit                  // Represents a bird weather submit event
	MQTTPublish                        // Represents an MQTT publish event
	SSEBroadcast                       // Represents a Server-Sent Events broadcast
)

// EventBehaviorFunc defines the signature for functions that determine the behavior of an event.
// It returns true if the event is allowed to be processed based on the given last event time and timeout.
type EventBehaviorFunc func(lastEventTime time.Time, timeout time.Duration) bool

// EventHandler holds the state and behavior for a specific event type.
type EventHandler struct {
	LastEventTime map[string]time.Time // Tracks the last event time for each species
	BehaviorFunc  EventBehaviorFunc    // Function that defines the event handling behavior
	Mutex         sync.Mutex           // Mutex to ensure thread-safe access
}

// NewEventHandler creates a new EventHandler with the specified timeout and behavior function.
func NewEventHandler(timeout time.Duration, behaviorFunc EventBehaviorFunc) *EventHandler {
	return &EventHandler{
		LastEventTime: make(map[string]time.Time),
		BehaviorFunc:  behaviorFunc,
	}
}

// shouldHandleEventLocked is a helper method that performs the event handling logic
// without locking. It assumes the caller already holds the Mutex lock.
// This eliminates duplication between ShouldHandleEvent and TrackEvent.
func (h *EventHandler) shouldHandleEventLocked(species string, timeout time.Duration) bool {
	// Normalize species name to lowercase for consistent key usage
	normalizedSpecies := strings.ToLower(species)

	lastTime, exists := h.LastEventTime[normalizedSpecies]
	if !exists || h.BehaviorFunc(lastTime, timeout) {
		h.LastEventTime[normalizedSpecies] = time.Now()
		return true
	}
	return false
}

// ShouldHandleEvent determines whether an event for a given species should be handled,
// based on the last event time and the specified timeout.
func (h *EventHandler) ShouldHandleEvent(species string, timeout time.Duration) bool {
	h.Mutex.Lock()
	defer h.Mutex.Unlock()

	return h.shouldHandleEventLocked(species, timeout)
}

// ResetEvent clears the last event time for a given species, effectively resetting its state.
func (h *EventHandler) ResetEvent(species string) {
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	delete(h.LastEventTime, strings.ToLower(species))
}

// StandardEventBehavior is a default behavior function that allows an event to be handled
// if the current time is greater than the last event time plus the timeout.
func StandardEventBehavior(lastEventTime time.Time, timeout time.Duration) bool {
	return time.Since(lastEventTime) >= timeout
}

// EventTracker manages event handling for different species across multiple event types.
type EventTracker struct {
	Handlers        map[EventType]*EventHandler
	SpeciesConfigs  map[string]conf.SpeciesConfig // Add this: Store species-specific configurations
	DefaultInterval time.Duration                 // Add this: Store the global default interval
	Mutex           sync.RWMutex                  // Mutex to ensure thread-safe access
}

// Add this new struct to hold configuration
type EventTrackerConfig struct {
	DatabaseSaveInterval      time.Duration
	LogToFileInterval         time.Duration
	NotificationInterval      time.Duration
	BirdWeatherSubmitInterval time.Duration
	MQTTPublishInterval       time.Duration
	SSEBroadcastInterval      time.Duration
}

// initEventTracker is a helper function that initializes an EventTracker with common setup
func initEventTracker(interval time.Duration, speciesConfigs map[string]conf.SpeciesConfig) *EventTracker {
	// Create normalized species configs map
	normalizedSpeciesConfigs := make(map[string]conf.SpeciesConfig)
	// Range is safe on nil maps, will iterate 0 times
	for species, config := range speciesConfigs {
		normalizedSpeciesConfigs[strings.ToLower(species)] = config
	}

	return &EventTracker{
		DefaultInterval: interval,
		Handlers: map[EventType]*EventHandler{
			DatabaseSave:      NewEventHandler(interval, StandardEventBehavior),
			LogToFile:         NewEventHandler(interval, StandardEventBehavior),
			SendNotification:  NewEventHandler(interval, StandardEventBehavior),
			BirdWeatherSubmit: NewEventHandler(interval, StandardEventBehavior),
			MQTTPublish:       NewEventHandler(interval, StandardEventBehavior),
			SSEBroadcast:      NewEventHandler(interval, StandardEventBehavior),
		},
		SpeciesConfigs: normalizedSpeciesConfigs, // Always initialized, even if empty
	}
}

// NewEventTracker creates a new EventTracker with the default interval
func NewEventTracker(interval time.Duration) *EventTracker {
	return initEventTracker(interval, nil)
}

// NewEventTrackerWithConfig creates a new EventTracker with a default interval and species-specific configurations.
func NewEventTrackerWithConfig(defaultInterval time.Duration, speciesConfigs map[string]conf.SpeciesConfig) *EventTracker {
	return initEventTracker(defaultInterval, speciesConfigs)
}

// TrackEvent checks if an event for a given species and event type should be processed.
// It utilizes the respective event handler to make this determination, considering species-specific intervals.
// This is a convenience wrapper around TrackEventWithNames for callers that only have the species name.
func (et *EventTracker) TrackEvent(species string, eventType EventType) bool {
	// Delegate to TrackEventWithNames with empty scientific name.
	// The fast path lookup by lowercase common name will produce identical behavior
	// to the previous direct map lookup.
	return et.TrackEventWithNames(species, "", eventType)
}

// TrackEventWithNames checks if an event for a given species (by common or scientific name) should be processed.
// This method supports lookup by both common name and scientific name, consistent with include/exclude matching.
func (et *EventTracker) TrackEventWithNames(commonName, scientificName string, eventType EventType) bool {
	// Determine tracking key: prefer common name, fall back to scientific name.
	// This ensures events are rate-limited per-species even if common name is missing.
	trackingKey := commonName
	if trackingKey == "" {
		trackingKey = scientificName
	}

	// If both names are empty, allow the event (can't rate-limit without a key)
	if trackingKey == "" {
		return true
	}

	normalizedTrackingKey := strings.ToLower(trackingKey)

	et.Mutex.RLock()

	handler, exists := et.Handlers[eventType]
	if !exists {
		et.Mutex.RUnlock()
		return false
	}

	// Determine the effective timeout for this species using both common and scientific name lookup
	effectiveTimeout := et.DefaultInterval

	// Use lookupSpeciesConfig to support both common name and scientific name
	if speciesConfig, found := lookupSpeciesConfig(et.SpeciesConfigs, commonName, scientificName); found {
		if speciesConfig.Interval > 0 {
			effectiveTimeout = time.Duration(speciesConfig.Interval) * time.Second
		} else if speciesConfig.Interval < 0 {
			log := GetLogger()
			log.Warn("Negative interval configured for species, using default interval instead",
				logger.Int("interval", speciesConfig.Interval),
				logger.String("commonName", commonName),
				logger.String("scientificName", scientificName))
		}
	}

	et.Mutex.RUnlock()

	handler.Mutex.Lock()
	allowEvent := handler.shouldHandleEventLocked(normalizedTrackingKey, effectiveTimeout)
	handler.Mutex.Unlock()

	return allowEvent
}

// ResetEvent resets the state for a specific species and event type, clearing any tracked event timing.
func (et *EventTracker) ResetEvent(species string, eventType EventType) {
	// Normalize species key consistently
	normalizedSpecies := strings.ToLower(species)

	// First lock EventTracker mutex to safely access handler map
	et.Mutex.RLock()
	handler, exists := et.Handlers[eventType]
	// Release EventTracker mutex before acquiring handler mutex to match lock ordering in TrackEvent
	et.Mutex.RUnlock()

	if exists {
		// Now lock handler mutex to update its state
		handler.ResetEvent(normalizedSpecies)
	}
}
