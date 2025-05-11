// eventtracker.go
package processor

import (
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// EventType represents the types of events to be tracked.
type EventType int

const (
	DatabaseSave      EventType = iota // Represents a database save event
	LogToFile                          // Represents a log to file event
	SendNotification                   // Represents a send notification event
	BirdWeatherSubmit                  // Represents a bird weather submit event
	MQTTPublish                        // Represents an MQTT publish event
)

// EventBehaviorFunc defines the signature for functions that determine the behavior of an event.
// It returns true if the event is allowed to be processed based on the given last event time and timeout.
type EventBehaviorFunc func(lastEventTime time.Time, timeout time.Duration) bool

// EventHandler holds the state and behavior for a specific event type.
type EventHandler struct {
	LastEventTime map[string]time.Time // Tracks the last event time for each species
	Timeout       time.Duration        // The minimum time interval between events
	BehaviorFunc  EventBehaviorFunc    // Function that defines the event handling behavior
	Mutex         sync.Mutex           // Mutex to ensure thread-safe access
}

// NewEventHandler creates a new EventHandler with the specified timeout and behavior function.
func NewEventHandler(timeout time.Duration, behaviorFunc EventBehaviorFunc) *EventHandler {
	return &EventHandler{
		LastEventTime: make(map[string]time.Time),
		Timeout:       timeout,
		BehaviorFunc:  behaviorFunc,
	}
}

// ShouldHandleEvent determines whether an event for a given species should be handled,
// based on the last event time and the specified timeout.
func (h *EventHandler) ShouldHandleEvent(species string) bool {
	h.Mutex.Lock()
	defer h.Mutex.Unlock()

	lastTime, exists := h.LastEventTime[species]
	if !exists || h.BehaviorFunc(lastTime, h.Timeout) {
		h.LastEventTime[species] = time.Now()
		return true
	}
	return false
}

// ResetEvent clears the last event time for a given species, effectively resetting its state.
func (h *EventHandler) ResetEvent(species string) {
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	delete(h.LastEventTime, species)
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
	Mutex           sync.Mutex                    // Mutex to ensure thread-safe access
}

// Add this new struct to hold configuration
type EventTrackerConfig struct {
	DatabaseSaveInterval      time.Duration
	LogToFileInterval         time.Duration
	NotificationInterval      time.Duration
	BirdWeatherSubmitInterval time.Duration
	MQTTPublishInterval       time.Duration
}

// Modify NewEventTracker to accept configuration
func NewEventTracker(interval time.Duration) *EventTracker {
	return &EventTracker{
		DefaultInterval: interval, // Store the default interval
		Handlers: map[EventType]*EventHandler{
			DatabaseSave:      NewEventHandler(interval, StandardEventBehavior),
			LogToFile:         NewEventHandler(interval, StandardEventBehavior),
			SendNotification:  NewEventHandler(interval, StandardEventBehavior),
			BirdWeatherSubmit: NewEventHandler(interval, StandardEventBehavior),
			MQTTPublish:       NewEventHandler(interval, StandardEventBehavior),
		},
		SpeciesConfigs: make(map[string]conf.SpeciesConfig), // Initialize the map
	}
}

// NewEventTrackerWithConfig creates a new EventTracker with a default interval and species-specific configurations.
func NewEventTrackerWithConfig(defaultInterval time.Duration, speciesConfigs map[string]conf.SpeciesConfig) *EventTracker {
	et := &EventTracker{
		DefaultInterval: defaultInterval,
		Handlers: map[EventType]*EventHandler{
			// Initialize handlers with the default interval.
			// TrackEvent will use specific intervals if configured.
			DatabaseSave:      NewEventHandler(defaultInterval, StandardEventBehavior),
			LogToFile:         NewEventHandler(defaultInterval, StandardEventBehavior),
			SendNotification:  NewEventHandler(defaultInterval, StandardEventBehavior),
			BirdWeatherSubmit: NewEventHandler(defaultInterval, StandardEventBehavior),
			MQTTPublish:       NewEventHandler(defaultInterval, StandardEventBehavior),
		},
		SpeciesConfigs: speciesConfigs, // Store species-specific configs
	}
	if et.SpeciesConfigs == nil { // Ensure the map is not nil
		et.SpeciesConfigs = make(map[string]conf.SpeciesConfig)
	}
	return et
}

// TrackEvent checks if an event for a given species and event type should be processed.
// It utilizes the respective event handler to make this determination, considering species-specific intervals.
func (et *EventTracker) TrackEvent(species string, eventType EventType) bool {
	et.Mutex.Lock() // Lock for reading SpeciesConfigs and accessing Handlers
	defer et.Mutex.Unlock()

	handler, exists := et.Handlers[eventType]
	if !exists {
		return false // Should not happen if EventTracker is initialized correctly
	}

	// Determine the effective timeout for this species and event type
	effectiveTimeout := et.DefaultInterval // Start with the global default

	if speciesConfig, ok := et.SpeciesConfigs[strings.ToLower(species)]; ok {
		if speciesConfig.Interval > 0 { // Check if a custom interval is set and valid
			effectiveTimeout = time.Duration(speciesConfig.Interval) * time.Second
		}
	}

	// The ShouldHandleEvent method of the handler will use its own configured timeout.
	// We need to pass the effectiveTimeout to the behavior function or adjust the handler's timeout.
	// For simplicity, let's adjust the handler's timeout temporarily if it's different.
	// This requires a change in EventHandler or how ShouldHandleEvent works.

	// Option 1: Pass timeout to ShouldHandleEvent (requires changing EventHandler.ShouldHandleEvent)
	// return handler.ShouldHandleEvent(species, effectiveTimeout)

	// Option 2: Temporarily modify handler's timeout (could be tricky with concurrency on EventHandler itself)
	// originalTimeout := handler.Timeout
	// handler.Timeout = effectiveTimeout
	// allow := handler.ShouldHandleEvent(species)
	// handler.Timeout = originalTimeout // Restore
	// return allow

	// Option 3: Create a one-off check using the behavior function directly.
	// This avoids modifying EventHandler deeply for now and keeps its internal timeout for other potential uses.
	handler.Mutex.Lock() // Lock the specific handler for its LastEventTime map
	lastTime, lastEventExists := handler.LastEventTime[species]
	allowEvent := !lastEventExists || handler.BehaviorFunc(lastTime, effectiveTimeout)
	if allowEvent {
		handler.LastEventTime[species] = time.Now()
	}
	handler.Mutex.Unlock() // Unlock the specific handler

	return allowEvent
}

// ResetEvent resets the state for a specific species and event type, clearing any tracked event timing.
func (et *EventTracker) ResetEvent(species string, eventType EventType) {
	et.Mutex.Lock()
	defer et.Mutex.Unlock()

	if handler, exists := et.Handlers[eventType]; exists {
		handler.ResetEvent(species)
	}
}
