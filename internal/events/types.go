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
	GetContext() map[string]any

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

	// SupportsBatching returns true if this consumer supports batch processing
	SupportsBatching() bool
}

// ResourceEventConsumer represents a consumer that processes resource monitoring events
type ResourceEventConsumer interface {
	EventConsumer

	// ProcessResourceEvent processes a single resource event
	ProcessResourceEvent(event ResourceEvent) error
}

// EventBusStats contains runtime statistics for monitoring
type EventBusStats struct {
	EventsReceived   uint64
	EventsSuppressed uint64
	EventsProcessed  uint64
	EventsDropped    uint64
	ConsumerErrors   uint64
	FastPathHits     uint64 // Number of times fast path was taken (no consumers)
}

// ResourceEvent represents a system resource monitoring event
type ResourceEvent interface {
	// GetResourceType returns the type of resource (cpu, memory, disk)
	GetResourceType() string

	// GetCurrentValue returns the current usage percentage
	GetCurrentValue() float64

	// GetThreshold returns the threshold that was crossed
	GetThreshold() float64

	// GetSeverity returns the severity level (warning, critical, recovery)
	GetSeverity() string

	// GetTimestamp returns when the event occurred
	GetTimestamp() time.Time

	// GetMetadata returns additional context data
	GetMetadata() map[string]any

	// GetMessage returns a human-readable message
	GetMessage() string

	// GetPath returns the path (for disk resources) or empty string
	GetPath() string
}
