// Package events provides an asynchronous event bus for decoupling error reporting
// from notification and telemetry systems, preventing blocking operations.
package events

import (
	"time"
)

// ErrorEvent represents an error event that can be processed asynchronously.
// This interface allows the errors package to push events without creating
// a circular dependency.
type ErrorEvent interface {
	// GetComponent returns the component that generated the error
	GetComponent() string
	
	// GetCategory returns the error category for grouping
	GetCategory() string
	
	// GetContext returns additional context data for the error
	GetContext() map[string]interface{}
	
	// GetTimestamp returns when the error occurred
	GetTimestamp() time.Time
	
	// GetError returns the underlying error
	GetError() error
	
	// GetMessage returns the error message
	GetMessage() string
	
	// IsReported returns whether this error has already been reported
	IsReported() bool
	
	// MarkReported marks the error as reported
	MarkReported()
}

// EventConsumer represents a consumer that processes error events
type EventConsumer interface {
	// Name returns the consumer name for identification
	Name() string
	
	// ProcessEvent processes a single error event
	ProcessEvent(event ErrorEvent) error
	
	// ProcessBatch processes multiple events at once (for efficiency)
	ProcessBatch(events []ErrorEvent) error
	
	// SupportsRatching returns true if this consumer supports batch processing
	SupportsBatching() bool
}

// EventBusStats contains runtime statistics for monitoring
type EventBusStats struct {
	EventsReceived   uint64
	EventsSuppressed uint64
	EventsProcessed  uint64
	EventsDropped    uint64
	ConsumerErrors   uint64
}