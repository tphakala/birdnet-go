// Package detection provides the core domain model for bird detection events.
// This package defines Result as the single source of truth for detection data
// used throughout the application. External serialization (API, MQTT, database)
// is handled by boundary-specific DTOs and entities.
package detection

import "time"

// Result represents a single bird detection event.
// This is the core domain model used throughout the application.
// External serialization is handled by boundary-specific DTOs.
type Result struct {
	// Identity (set after database save)
	ID uint

	// Timestamp with timezone - single source of truth for when detection occurred
	// Replaces separate Date/Time strings, removes ambiguity
	Timestamp time.Time

	// Source information
	SourceNode  string      // Node name (for multi-node setups)
	AudioSource AudioSource // Rich audio source metadata

	// Audio clip timing (within the source stream)
	BeginTime time.Time
	EndTime   time.Time

	// Species identification
	Species    Species
	Confidence float64

	// Location where detection occurred
	Latitude  float64
	Longitude float64

	// Analysis parameters used
	Threshold   float64
	Sensitivity float64

	// AI Model information
	Model ModelInfo

	// Output
	ClipName       string        // Saved audio clip filename
	ProcessingTime time.Duration // How long analysis took

	// Runtime-only data (not persisted)
	Occurrence float64 // Probability 0-1 based on location/time/season

	// Review status (populated from DB relations when loaded)
	Verified string
	Locked   bool
	Comments []Comment
}

// Comment represents a user comment on a detection.
type Comment struct {
	ID        uint
	Entry     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AdditionalResult represents a secondary species prediction from the same audio chunk.
// BirdNET may return multiple species predictions for a single 3-second analysis window.
// The primary (highest confidence) result is in Result.Species/Confidence.
// Additional predictions are stored separately for reference.
type AdditionalResult struct {
	Species    Species
	Confidence float64
}

// Date returns the detection date in YYYY-MM-DD format.
// This is a convenience method for APIs that need the legacy date format.
func (r *Result) Date() string {
	return r.Timestamp.Format("2006-01-02")
}

// Time returns the detection time in HH:MM:SS format.
// This is a convenience method for APIs that need the legacy time format.
func (r *Result) Time() string {
	return r.Timestamp.Format("15:04:05")
}
