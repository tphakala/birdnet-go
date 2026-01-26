package repository

import "github.com/tphakala/birdnet-go/internal/datastore/v2/entities"

// VerificationFilter wraps VerificationStatus for search filters.
type VerificationFilter entities.VerificationStatus

// SearchFilters provides advanced search criteria for detections.
// All timestamps are Unix int64 for consistency and indexing performance.
type SearchFilters struct {
	// Query provides text search across scientific names.
	Query string

	// LabelIDs filters by specific label IDs.
	LabelIDs []uint

	// ModelID filters by AI model (optional).
	ModelID *uint

	// AudioSourceIDs filters by audio sources (optional).
	// Supports multiple sources for location filtering.
	AudioSourceIDs []uint

	// StartTime filters detections on or after this Unix timestamp (optional).
	StartTime *int64

	// EndTime filters detections on or before this Unix timestamp (optional).
	EndTime *int64

	// IncludedHours filters by hour of day (0-23).
	// Supports non-contiguous ranges (e.g., dawn + night = [5,6,20,21,22,23,0,1,2,3,4]).
	IncludedHours []int

	// TimezoneOffset is the timezone offset in seconds for hour calculation.
	// Used with IncludedHours to extract local hour from Unix timestamp.
	TimezoneOffset int

	// MinConfidence filters detections with confidence >= this value (optional).
	MinConfidence *float64

	// MaxConfidence filters detections with confidence <= this value (optional).
	MaxConfidence *float64

	// Verified filters by specific verification status (optional).
	Verified *VerificationFilter

	// IsReviewed filters by review existence (optional).
	// true = has review with verdict, false = no review or no verdict.
	IsReviewed *bool

	// IsLocked filters by lock status (optional).
	IsLocked *bool

	// SortBy specifies sort field: "detected_at" or "confidence".
	SortBy string

	// SortDesc specifies descending sort order when true.
	SortDesc bool

	// Limit specifies maximum results to return.
	Limit int

	// Offset specifies number of results to skip.
	Offset int

	// MinID filters to records with ID > MinID (cursor-based pagination).
	MinID uint
}

// ModelStats contains statistics for a specific AI model.
type ModelStats struct {
	// ModelID is the AI model identifier.
	ModelID uint

	// TotalDetections is the total number of detections for this model.
	TotalDetections int64

	// UniqueSpecies is the count of distinct species detected by this model.
	UniqueSpecies int64

	// FirstDetection is the Unix timestamp of the first detection.
	FirstDetection int64

	// LastDetection is the Unix timestamp of the most recent detection.
	LastDetection int64

	// AvgConfidence is the average confidence score across all detections.
	AvgConfidence float64
}

// SpeciesModelStats contains species statistics for a specific model.
type SpeciesModelStats struct {
	// LabelID is the label identifier.
	LabelID uint

	// ModelID is the AI model identifier.
	ModelID uint

	// TotalDetections is the total number of detections for this species/model.
	TotalDetections int64

	// FirstDetection is the Unix timestamp of the first detection.
	FirstDetection int64

	// LastDetection is the Unix timestamp of the most recent detection.
	LastDetection int64

	// AvgConfidence is the average confidence score.
	AvgConfidence float64
}

// SpeciesCount represents a species with its detection count.
type SpeciesCount struct {
	// LabelID is the label identifier.
	LabelID uint

	// ScientificName is the species scientific name.
	ScientificName string

	// Count is the number of detections.
	Count int64
}

// DailyCount represents detection count for a specific date.
// Note: Date uses string format (YYYY-MM-DD) rather than Unix timestamp
// because daily aggregations are timezone-dependent and the string format
// is more natural for display and grouping purposes.
type DailyCount struct {
	// Date in YYYY-MM-DD format (local timezone).
	Date string

	// Count is the number of detections on this date.
	Count int64
}

// SpeciesFirstSeen represents the first detection of a species in a time period.
type SpeciesFirstSeen struct {
	// LabelID is the label identifier.
	LabelID uint

	// ScientificName is the species scientific name.
	ScientificName string

	// FirstDetected is the Unix timestamp of first detection in the period.
	FirstDetected int64

	// DetectionID is the ID of the first detection.
	DetectionID uint
}

// SpeciesSummaryData contains summary statistics for a species.
type SpeciesSummaryData struct {
	// LabelID is the label identifier.
	LabelID uint

	// ScientificName is the species scientific name.
	ScientificName string

	// TotalDetections is the count of detections.
	TotalDetections int64

	// FirstDetection is the Unix timestamp of first detection.
	FirstDetection int64

	// LastDetection is the Unix timestamp of most recent detection.
	LastDetection int64

	// AvgConfidence is the average confidence score.
	AvgConfidence float64

	// MaxConfidence is the highest confidence score.
	MaxConfidence float64
}

// HourlyDistributionData contains detection counts by hour.
type HourlyDistributionData struct {
	// Hour is 0-23.
	Hour int

	// Count is the number of detections in this hour.
	Count int64
}

// DailyAnalyticsData contains daily analytics information.
type DailyAnalyticsData struct {
	// Date in YYYY-MM-DD format.
	Date string

	// TotalDetections is the count of detections.
	TotalDetections int64

	// UniqueSpecies is the count of distinct species.
	UniqueSpecies int64

	// AvgConfidence is the average confidence score.
	AvgConfidence float64
}

// NewSpeciesData contains information about a newly detected species.
type NewSpeciesData struct {
	// LabelID is the label identifier.
	LabelID uint

	// ScientificName is the species scientific name.
	ScientificName string

	// FirstDetected is the Unix timestamp of the very first detection.
	FirstDetected int64

	// DetectionID is the ID of the first detection.
	DetectionID uint

	// Confidence is the confidence score of the first detection.
	Confidence float64
}
