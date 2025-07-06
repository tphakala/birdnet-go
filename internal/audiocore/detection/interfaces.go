// Package detection provides interfaces and implementations for handling
// audio analysis results and triggering actions based on detections.
package detection

import (
	"context"
	"time"
)

// Detection represents a single detection event
type Detection struct {
	// SourceID identifies the audio source
	SourceID string

	// Timestamp when the detection occurred
	Timestamp time.Time

	// Species detected (e.g., "Turdus_migratorius_American Robin")
	Species string

	// Confidence score (0.0 to 1.0)
	Confidence float32

	// StartTime within the analyzed chunk (seconds)
	StartTime float64

	// EndTime within the analyzed chunk (seconds)
	EndTime float64

	// Additional metadata
	Metadata map[string]interface{}
}

// AnalysisResult contains the results of analyzing an audio chunk
type AnalysisResult struct {
	// SourceID identifies the audio source
	SourceID string

	// Timestamp of the audio chunk
	Timestamp time.Time

	// Duration of the analyzed chunk
	Duration time.Duration

	// Detections found in the chunk
	Detections []Detection

	// AnalyzerID that produced this result
	AnalyzerID string

	// Error if analysis failed
	Error error
}

// Handler processes detection events
type Handler interface {
	// HandleDetection processes a single detection
	HandleDetection(ctx context.Context, detection *Detection) error

	// HandleAnalysisResult processes a complete analysis result
	HandleAnalysisResult(ctx context.Context, result *AnalysisResult) error

	// ID returns the handler's identifier
	ID() string

	// Close releases any resources
	Close() error
}

// HandlerChain manages multiple detection handlers
type HandlerChain interface {
	// AddHandler adds a handler to the chain
	AddHandler(handler Handler) error

	// RemoveHandler removes a handler by ID
	RemoveHandler(id string) error

	// HandleDetection sends detection to all handlers
	HandleDetection(ctx context.Context, detection *Detection) error

	// HandleAnalysisResult sends result to all handlers
	HandleAnalysisResult(ctx context.Context, result *AnalysisResult) error

	// GetHandlers returns all handlers in order
	GetHandlers() []Handler

	// Close closes all handlers
	Close() error
}

// Config contains configuration for detection handling
type Config struct {
	// MinConfidence is the minimum confidence threshold
	MinConfidence float32

	// EnabledHandlers lists which handlers to activate
	EnabledHandlers []string

	// HandlerConfigs contains handler-specific configuration
	HandlerConfigs map[string]interface{}
}
