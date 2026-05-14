// Package datastore provides database operations for BirdNET-Go.
package datastore

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// defaultDetectionLimit is the default number of detections returned by queries.
const defaultDetectionLimit = 100

// DetectionRepository defines the interface for detection persistence operations.
// This interface uses the domain model (detection.Result) and abstracts away
// the database-specific implementation details.
type DetectionRepository interface {
	// Save persists a detection result and its additional predictions.
	// The result's ID is updated after successful save.
	Save(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error

	// Get retrieves a detection by ID.
	// Returns the detection with its comments, review status, and lock state populated.
	Get(ctx context.Context, id string) (*detection.Result, error)

	// Delete removes a detection by ID.
	// Returns an error if the detection is locked.
	Delete(ctx context.Context, id string) error

	// GetRecent retrieves the most recent detections.
	GetRecent(ctx context.Context, limit int) ([]*detection.Result, error)

	// Search finds detections matching the given filters.
	// Returns results, total count, and any error.
	Search(ctx context.Context, filters *DetectionFilters) ([]*detection.Result, int64, error)

	// GetBySpecies retrieves detections for a specific species.
	GetBySpecies(ctx context.Context, species string, filters *DetectionFilters) ([]*detection.Result, int64, error)

	// GetByDateRange retrieves detections within a date range.
	GetByDateRange(ctx context.Context, startDate, endDate string, limit, offset int) ([]*detection.Result, int64, error)

	// GetHourly retrieves detections for a specific hour on a date.
	GetHourly(ctx context.Context, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error)

	// Lock/Unlock operations
	Lock(ctx context.Context, id string) error
	Unlock(ctx context.Context, id string) error
	IsLocked(ctx context.Context, id string) (bool, error)

	// Review operations
	SetReview(ctx context.Context, id, verified string) error
	GetReview(ctx context.Context, id string) (string, error)

	// CorrectSpecies replaces a detection's species + confidence and marks
	// the detection as verified='correct' in one logical operation. The
	// detection must be unlocked; concurrent lock acquisition between
	// caller-side checks and this write is reported as ErrDetectionLocked.
	// Implementations that wrap the v2 schema use params.Model to write
	// the matching v2_ai_models / v2_labels rows; v1-only implementations
	// ignore params.Model (the v1 notes table has no model_id column).
	CorrectSpecies(ctx context.Context, id string, params *CorrectSpeciesParams) error

	// Comment operations
	AddComment(ctx context.Context, id, comment string) error
	GetComments(ctx context.Context, id string) ([]detection.Comment, error)
	UpdateComment(ctx context.Context, commentID uint, entry string) error
	DeleteComment(ctx context.Context, commentID uint) error

	// GetClipPath returns the audio clip path for a detection.
	GetClipPath(ctx context.Context, id string) (string, error)

	// GetAdditionalResults returns the secondary predictions for a detection.
	GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error)

	// CountAll returns the total count of all detections.
	// This is a lightweight count operation that doesn't load any data.
	CountAll(ctx context.Context) (int64, error)
}

// CorrectSpeciesParams carries the fields needed to apply an operator-driven
// species correction. Returned alongside CorrectSpecies on DetectionRepository.
type CorrectSpeciesParams struct {
	// ScientificName is the binomial Latin name to write to the detection,
	// e.g. "Ficedula hypoleuca". Required.
	ScientificName string

	// CommonName is the locale-appropriate common name to write to the
	// detection, resolved by the caller from the orchestrator's label
	// resolver (so v3 geomodel translations stay consistent with the rest
	// of the UI). Required.
	CommonName string

	// Confidence is the value to record on the corrected detection. Range
	// [0, 1]; typically the max confidence the chosen model produced for
	// this species in the reanalysis pass.
	Confidence float64

	// Model identifies the classifier the operator chose. Consumed by
	// v2-aware implementations to look up the matching v2_ai_models row
	// (and then the (scientific, model) → v2_labels row to write to
	// v2_detections.label_id). v1-only implementations ignore this; the
	// v1 notes table has no model_id column.
	Model detection.ModelInfo
}

// DetectionFilters defines the filter parameters for detection queries.
type DetectionFilters struct {
	// Text search
	Query string

	// Species filter
	Species []string

	// Date filters
	Date      string
	StartDate string
	EndDate   string

	// Time filters
	Hour      string
	HourRange *HourRange
	TimeOfDay []string // "day", "night", "sunrise", "sunset"

	// Confidence filter
	Confidence *ConfidenceRange

	// Status filters
	Verified *bool
	Locked   *bool

	// Location filter
	Location []string

	// Pagination
	Limit  int
	Offset int

	// Cursor-based pagination (for migration worker)
	// MinID filters to records with ID > MinID, enabling efficient cursor-based iteration.
	MinID uint
	// CursorPagination indicates this query uses cursor-based pagination and must
	// sort by id ASC. Set automatically by WithMinID.
	CursorPagination bool

	// Sort order
	SortAscending bool
}

// HourRange defines a time range in hours.
type HourRange struct {
	Start int
	End   int
}

// ConfidenceRange defines a confidence filter with operator.
type ConfidenceRange struct {
	Operator string // ">=", "<=", "=", ">", "<"
	Value    float64
}

// NewDetectionFilters creates default detection filters.
func NewDetectionFilters() *DetectionFilters {
	return &DetectionFilters{
		Limit:         defaultDetectionLimit,
		Offset:        0,
		SortAscending: false,
	}
}

// WithLimit sets the result limit.
func (f *DetectionFilters) WithLimit(limit int) *DetectionFilters {
	f.Limit = limit
	return f
}

// WithOffset sets the pagination offset.
func (f *DetectionFilters) WithOffset(offset int) *DetectionFilters {
	f.Offset = offset
	return f
}

// WithSpecies adds species filters.
func (f *DetectionFilters) WithSpecies(species ...string) *DetectionFilters {
	f.Species = append(f.Species, species...)
	return f
}

// WithDateRange sets the date range filter.
func (f *DetectionFilters) WithDateRange(start, end string) *DetectionFilters {
	f.StartDate = start
	f.EndDate = end
	return f
}

// WithConfidence sets the confidence filter.
func (f *DetectionFilters) WithConfidence(operator string, value float64) *DetectionFilters {
	f.Confidence = &ConfidenceRange{Operator: operator, Value: value}
	return f
}

// WithVerified sets the verification status filter.
func (f *DetectionFilters) WithVerified(verified bool) *DetectionFilters {
	f.Verified = &verified
	return f
}

// WithLocked sets the locked status filter.
func (f *DetectionFilters) WithLocked(locked bool) *DetectionFilters {
	f.Locked = &locked
	return f
}

// WithMinID sets the minimum ID for cursor-based pagination.
// When set, only detections with ID > MinID are returned, ordered by ID ASC.
func (f *DetectionFilters) WithMinID(id uint) *DetectionFilters {
	f.MinID = id
	f.CursorPagination = true // Cursor pagination must sort by id ASC
	f.SortAscending = true    // MinID implies ascending order for proper iteration
	return f
}

// Timezone returns the configured timezone for timestamp conversions.
// This should be called by repository implementations when converting
// between domain models and database entities.
func Timezone() *time.Location {
	// Default to local timezone
	// In future this could be made configurable via settings
	return time.Local
}
