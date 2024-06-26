// eventtracker.go
package processor

import (
	"sync"
	"time"
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
	Handlers map[EventType]*EventHandler // Map of event types to their respective handlers
	Mutex    sync.Mutex                  // Mutex to ensure thread-safe access
}

// NewEventTracker initializes a new EventTracker with default event handlers.
func NewEventTracker() *EventTracker {
	return &EventTracker{
		Handlers: map[EventType]*EventHandler{
			DatabaseSave:      NewEventHandler(15*time.Second, StandardEventBehavior),
			LogToFile:         NewEventHandler(15*time.Second, StandardEventBehavior),
			SendNotification:  NewEventHandler(60*time.Minute, StandardEventBehavior),
			BirdWeatherSubmit: NewEventHandler(15*time.Second, StandardEventBehavior),
		},
	}
}

// TrackEvent checks if an event for a given species and event type should be processed.
// It utilizes the respective event handler to make this determination.
func (et *EventTracker) TrackEvent(species string, eventType EventType) bool {
	et.Mutex.Lock()
	defer et.Mutex.Unlock()

	handler, exists := et.Handlers[eventType]
	if !exists {
		return false
	}
	return handler.ShouldHandleEvent(species)
}

// ResetEvent resets the state for a specific species and event type, clearing any tracked event timing.
func (et *EventTracker) ResetEvent(species string, eventType EventType) {
	et.Mutex.Lock()
	defer et.Mutex.Unlock()

	if handler, exists := et.Handlers[eventType]; exists {
		handler.ResetEvent(species)
	}
}
