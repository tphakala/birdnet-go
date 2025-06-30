// Package errors - event bus integration
package errors

import (
	"sync/atomic"
)

// EventPublisher is an interface for publishing error events
// This interface allows the errors package to publish events without
// importing the events package, avoiding circular dependencies
type EventPublisher interface {
	TryPublish(event any) bool
}

// Global event publisher (set by the events package)
var globalEventPublisher atomic.Value // stores EventPublisher

// SetEventPublisher sets the global event publisher
// This should be called by the events package during initialization
func SetEventPublisher(publisher EventPublisher) {
	globalEventPublisher.Store(publisher)
}

// publishToEventBus publishes an error to the event bus if available
func publishToEventBus(ee *EnhancedError) {
	// Load the publisher atomically
	publisher := globalEventPublisher.Load()
	if publisher == nil {
		return
	}
	
	eventPublisher := publisher.(EventPublisher)
	
	// Try to publish the event
	// The event bus will handle type assertion to ErrorEvent interface
	eventPublisher.TryPublish(ee)
}

// reportToTelemetry has been updated to use the event bus
// This is the new implementation that replaces direct telemetry calls
func reportToTelemetry(ee *EnhancedError) {
	// Skip entirely if nothing to do
	if !hasActiveReporting.Load() {
		return
	}
	
	// Try to publish to event bus first
	if globalEventPublisher.Load() != nil {
		// Event bus is available, use async processing
		publishToEventBus(ee)
		return
	}
	
	// Fall back to legacy synchronous processing
	reportToTelemetryLegacy(ee)
}