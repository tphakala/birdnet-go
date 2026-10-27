// Package datastore provides database operations for BirdNET-Go.
package datastore

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/mapper"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// ErrNilAdvancedSearchFilters is returned by DetectionRepository.SearchAdvanced when it
// is called without filters.
var ErrNilAdvancedSearchFilters = errors.NewStd("advanced search filters must not be nil")

// DefaultDetectionLimit is the default number of detections returned by queries.
const DefaultDetectionLimit = 100

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

	// SearchAdvanced finds detections matching the filters as given, including the sort
	// order, scientific-name list and source list that DetectionFilters cannot express; this
	// is the query the detection list API builds from request parameters. SortBy
	// SortBySearchDefault orders as the plain text search does: newest first by detection
	// time on the v2 store and by id on the legacy store. Unlike the plain text search on the
	// legacy store, an empty query does not drop rows whose names are both NULL. A nil
	// filters returns ErrNilAdvancedSearchFilters. A Limit of zero or less is not capped:
	// every match is returned. Returns results, total count before pagination, and any
	// error.
	SearchAdvanced(ctx context.Context, filters *AdvancedSearchFilters) ([]*detection.Result, int64, error)

	// GetBySpecies retrieves detections for a specific species, newest first, with the
	// total count of matches before pagination. An empty date means all dates; a date with
	// an empty hour means that whole day; a date with an hour restricts to duration hours
	// from that hour. A limit of zero or less means DefaultDetectionLimit. The legacy store
	// matches only rows whose date equals the given date, so on it an empty date finds
	// nothing. The v2 store ignores a date that is not YYYY-MM-DD, as it ignores an hour
	// without a date, and returns every date, so callers must validate the date first. A
	// duration of zero means one hour on the v2 store and an empty window on the legacy
	// store; callers should pass a positive duration.
	GetBySpecies(ctx context.Context, species, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error)

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

	// Comment operations
	AddComment(ctx context.Context, id, comment string) error
	GetComments(ctx context.Context, id string) ([]detection.Comment, error)
	UpdateComment(ctx context.Context, commentID uint, entry string) error
	DeleteComment(ctx context.Context, commentID uint) error

	// GetClipPath returns the audio clip path for a detection.
	GetClipPath(ctx context.Context, id string) (string, error)

	// GetAdditionalResults returns the secondary predictions for a detection.
	GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error)

	// RecordClipSampleRate sets the clip sample rate (Hz) of the detection while its
	// clip_name still equals clipName, overwriting any stored value. The legacy
	// store ignores the call.
	RecordClipSampleRate(ctx context.Context, id uint, clipName string, rate int) error

	// FillClipSampleRate sets the clip sample rate (Hz) only while it is NULL and
	// clip_name still equals clipName. It reports whether a row changed. The
	// legacy store ignores the call and reports false.
	FillClipSampleRate(ctx context.Context, id uint, clipName string, rate int) (bool, error)

	// CountAll returns the total count of all detections.
	// This is a lightweight count operation that doesn't load any data.
	CountAll(ctx context.Context) (int64, error)
}

// DetectionFilters defines the filter parameters for detection queries.
type DetectionFilters struct {
	// Text search
	Query string

	// Species filter
	Species []string

	// Date filters
	StartDate string
	EndDate   string

	// Time filters
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
		Limit:         DefaultDetectionLimit,
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

// ToAdvancedSearchFilters converts the filters to the AdvancedSearchFilters both stores
// accept. StartDate and EndDate are parsed as dates in tz, so tz must not be nil. A nil
// receiver returns the default limit and no other filter. When either date is set the
// range is always bounded on both sides: an empty StartDate is the zero time and an
// empty EndDate is the current time in tz.
func (f *DetectionFilters) ToAdvancedSearchFilters(tz *time.Location) (AdvancedSearchFilters, error) {
	if f == nil {
		return AdvancedSearchFilters{
			Limit:  DefaultDetectionLimit,
			Offset: 0,
		}, nil
	}

	legacy := AdvancedSearchFilters{
		TextQuery:        f.Query,
		Species:          f.Species,
		TimeOfDay:        f.TimeOfDay,
		Location:         f.Location,
		Limit:            f.Limit,
		Offset:           f.Offset,
		SortAscending:    f.SortAscending,
		Verified:         f.Verified,
		Locked:           f.Locked,
		MinID:            f.MinID,
		CursorPagination: f.CursorPagination,
	}

	// Convert date range - parse strings to time.Time using the given timezone
	if f.StartDate != "" || f.EndDate != "" {
		dateRange, err := parseDateRange(f.StartDate, f.EndDate, tz)
		if err != nil {
			return AdvancedSearchFilters{}, err
		}
		legacy.DateRange = dateRange
	}

	// Convert hour range
	if f.HourRange != nil {
		legacy.Hour = &HourFilter{
			Start: f.HourRange.Start,
			End:   f.HourRange.End,
		}
	}

	// Convert confidence
	if f.Confidence != nil {
		legacy.Confidence = &ConfidenceFilter{
			Operator: f.Confidence.Operator,
			Value:    f.Confidence.Value,
		}
	}

	return legacy, nil
}

// DateRangeSearchFilters builds the AdvancedSearchFilters for a date range query.
// startDate and endDate are dates in tz, which must not be nil. Both bounds are always
// set: an empty startDate is the zero time and an empty endDate is the current time in tz.
func DateRangeSearchFilters(startDate, endDate string, limit, offset int, tz *time.Location) (AdvancedSearchFilters, error) {
	dateRange, err := parseDateRange(startDate, endDate, tz)
	if err != nil {
		return AdvancedSearchFilters{}, err
	}
	return AdvancedSearchFilters{
		DateRange: dateRange,
		Limit:     limit,
		Offset:    offset,
	}, nil
}

// parseDateRange parses both bounds of a date range in tz.
func parseDateRange(startDate, endDate string, tz *time.Location) (*DateRange, error) {
	start, err := parseDateWithDefault(startDate, false, tz)
	if err != nil {
		return nil, err
	}
	end, err := parseDateWithDefault(endDate, true, tz)
	if err != nil {
		return nil, err
	}
	return &DateRange{Start: start, End: end}, nil
}

// parseDateWithDefault parses a date string, returning a default if empty.
// If defaultToNow is true and the string is empty, returns current time in tz.
func parseDateWithDefault(dateStr string, defaultToNow bool, tz *time.Location) (time.Time, error) {
	if dateStr == "" {
		if defaultToNow {
			return time.Now().In(tz), nil
		}
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation(mapper.DateFormat, dateStr, tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q: %w", dateStr, err)
	}
	return t, nil
}
