// Package detection provides domain models for bird detection processing.
// These models are optimized for runtime use and are independent of database schema.
//
// The detection package implements the Repository pattern to decouple business logic
// from database persistence, enabling:
//   - Database schema normalization without breaking runtime code
//   - Improved testability through mock repositories
//   - Clear separation between domain and persistence concerns
//   - Flexible caching strategies for performance optimization
package detection

import (
	"fmt"
	"time"
)

// Detection represents a bird detection at runtime.
// This is the domain model used throughout the detection processing flow.
// It contains both persisted data and runtime-only metadata.
type Detection struct {
	// Identity
	ID         uint   // Database ID (populated after save, 0 for new detections)
	SourceNode string // Node name where detection originated

	// Species Information
	Species        *Species // Pointer allows lazy loading from cache
	SpeciesCode    string   // eBird taxonomy code (e.g., "amecro")
	ScientificName string   // Scientific name (e.g., "Corvus brachyrhynchos")
	CommonName     string   // Common name (e.g., "American Crow")

	// Temporal Information
	Date      string    // Detection date in ISO 8601 format (YYYY-MM-DD)
	Time      string    // Detection time in 24-hour format (HH:MM:SS)
	BeginTime time.Time // Exact start time of detection
	EndTime   time.Time // Exact end time of detection

	// Audio Source Information
	// This is runtime metadata that helps identify where the detection came from.
	// It is NOT persisted to the database (only SourceNode is saved).
	Source AudioSource

	// Analysis Results
	Confidence  float64 // Detection confidence (0.0-1.0, rounded to 2 decimals)
	Threshold   float64 // Threshold setting used for detection
	Sensitivity float64 // Sensitivity setting used for detection

	// Occurrence Probability
	// This is a runtime-only field calculated based on location and time.
	// It represents the likelihood of the species being present (0.0-1.0).
	// NOT persisted to database as it can be recalculated on demand.
	Occurrence float64

	// Geographic Information
	Latitude  float64 // Geographic latitude
	Longitude float64 // Geographic longitude

	// Audio Clip Information
	ClipName       string        // Audio clip filename/path
	ProcessingTime time.Duration // Time taken to process this detection

	// Review Status
	// These fields are populated from database relationships and represent
	// the user's review/verification of the detection.
	Verified string // Review status: "correct", "false_positive", or empty for unverified
	Locked   bool   // Whether detection is locked from editing

	// Relationships
	// These are lazy-loaded when needed to avoid unnecessary database queries.
	Predictions []Prediction // All BirdNET predictions, not just the top match
	Comments    []Comment    // User comments on this detection
}

// Prediction represents a single species identification result from BirdNET.
// A detection typically contains 5-10 predictions, with the top prediction
// becoming the actual Detection.Species.
type Prediction struct {
	Species    *Species // Predicted species (may differ from Detection.Species)
	Confidence float64  // Confidence level for this prediction (0.0-1.0)
	Rank       int      // Position in the prediction list (1-indexed, 1 = highest confidence)
}

// Species represents a bird species in the system.
// Species data is normalized and cached in memory for performance.
type Species struct {
	ID             uint   // Database ID
	SpeciesCode    string // eBird taxonomy code (e.g., "amecro", "norcar")
	ScientificName string // Scientific name (e.g., "Corvus brachyrhynchos")
	CommonName     string // Default common name, typically English (e.g., "American Crow")
	// Future enhancement: Support multilingual names in separate table
}

// AudioSource represents a structured audio source with ID, safe string, and display name.
// This allows safe separation of concerns:
//   - ID: Used for buffer operations and internal tracking (e.g., "rtsp_87b89761")
//   - SafeString: Sanitized connection string for logging (credentials removed)
//   - DisplayName: User-friendly name for UI display
//
// This is runtime-only metadata and is NOT persisted to the database.
// The database only stores SourceNode (the device/node name).
type AudioSource struct {
	ID          string // Source ID for buffer operations
	SafeString  string // Sanitized connection string for logging (credentials removed)
	DisplayName string // User-friendly name for UI display
}

// Comment represents a user comment on a detection.
type Comment struct {
	ID          uint      // Database ID
	DetectionID uint      // Foreign key to Detection
	Entry       string    // Comment text
	CreatedAt   time.Time // When comment was created
	UpdatedAt   time.Time // When comment was last updated
}

// Review represents the review/verification status of a detection.
type Review struct {
	ID          uint      // Database ID
	DetectionID uint      // Foreign key to Detection
	Verified    string    // "correct" or "false_positive"
	CreatedAt   time.Time // When review was created
	UpdatedAt   time.Time // When review was last updated
}

// Lock represents the lock status of a detection.
// Locked detections cannot be modified or deleted.
type Lock struct {
	ID          uint      // Database ID
	DetectionID uint      // Foreign key to Detection
	LockedAt    time.Time // When detection was locked
}

// NewDetection creates a new detection with the provided parameters and validates input.
// Returns an error if required fields are missing or values are out of acceptable ranges.
// This is a convenience constructor for creating detections in the processing pipeline.
func NewDetection(
	sourceNode string,
	date, timeStr string,
	beginTime, endTime time.Time,
	source AudioSource,
	speciesCode, scientificName, commonName string,
	confidence, threshold, sensitivity float64,
	latitude, longitude float64,
	clipName string,
	processingTime time.Duration,
	occurrence float64,
) (*Detection, error) {
	// Validate required fields
	if sourceNode == "" {
		return nil, fmt.Errorf("sourceNode cannot be empty")
	}
	if scientificName == "" && commonName == "" {
		return nil, fmt.Errorf("either scientificName or commonName must be provided")
	}

	// Validate ranges
	if confidence < 0.0 || confidence > 1.0 {
		return nil, fmt.Errorf("confidence must be between 0.0 and 1.0, got %f", confidence)
	}
	if occurrence < 0.0 || occurrence > 1.0 {
		return nil, fmt.Errorf("occurrence must be between 0.0 and 1.0, got %f", occurrence)
	}
	if !endTime.IsZero() && !beginTime.IsZero() && endTime.Before(beginTime) {
		return nil, fmt.Errorf("endTime cannot be before beginTime")
	}

	return &Detection{
		SourceNode:     sourceNode,
		Date:           date,
		Time:           timeStr,
		BeginTime:      beginTime,
		EndTime:        endTime,
		Source:         source,
		SpeciesCode:    speciesCode,
		ScientificName: scientificName,
		CommonName:     commonName,
		Confidence:     confidence,
		Threshold:      threshold,
		Sensitivity:    sensitivity,
		Latitude:       latitude,
		Longitude:      longitude,
		ClipName:       clipName,
		ProcessingTime: processingTime,
		Occurrence:     occurrence,
	}, nil
}
