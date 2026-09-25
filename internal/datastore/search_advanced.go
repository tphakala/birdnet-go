// Package datastore provides advanced search functionality
package datastore

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// SortBySearchDefault preserves each datastore implementation's historical
// ordering for the simple detection-search endpoint when it needs advanced
// predicates. Legacy notes sort by ID; the normalized datastore sorts by time.
const SortBySearchDefault = "search_default"

// AdvancedSearchFilters represents all possible search filters from the frontend
// sourceNodeInPredicate selects notes by the node that recorded them; both the location and
// source filters resolve to it on the legacy schema.
const sourceNodeInPredicate = "source_node IN ?"

type AdvancedSearchFilters struct {
	TextQuery string
	// SpeciesScientific contains exact scientific names that are OR-ed with
	// TextQuery. It lets the API expand active-locale common-name substrings while
	// preserving raw scientific/common-name substring matching.
	SpeciesScientific []string
	Confidence        *ConfidenceFilter
	// ConfidenceRange expresses an inclusive [Min, Max] confidence band, which the
	// single-operator Confidence filter cannot. When both are set, both apply
	// (they are AND-ed), so a caller should normally supply only one.
	ConfidenceRange *ConfidenceRangeFilter
	// TimeOfDay names the periods to keep. The canonical vocabulary is the one the
	// simple search path uses -- "day", "night", "sunrise", "sunset" (see the
	// TimeOfDay* constants) -- resolved against the station's real sun events.
	// "dawn" and "dusk" are accepted as legacy aliases for "sunrise" and "sunset";
	// NormalizeTimeOfDayPeriods maps them before the filter is applied.
	TimeOfDay []string
	Hour      *HourFilter
	DateRange *DateRange
	// Verified is the legacy two-state review filter: true selects detections that
	// carry any verdict, false selects those that carry none. It cannot express
	// "false positives only", so prefer VerifiedStatus. When VerifiedStatus is set
	// it wins and this field is ignored.
	Verified *bool
	// VerifiedStatus selects by review verdict: "" or "any" (no filter), "correct",
	// "false_positive", or "unverified" (see the VerifiedStatus* constants).
	VerifiedStatus string
	Species        []string
	Location       []string // Maps to source_node column
	// Source restricts results to audio sources, each given as the source's numeric ID (as listed
	// by /analytics/sources), its display name, node name, or source URI. The v2 store resolves it
	// against the audio_sources table; the legacy store, which records only the node name per
	// note, matches it against source_node, so only node-name values select rows there.
	Source        []string
	Locked        *bool
	SortAscending bool
	SortBy        string // "date_desc", "date_asc", "species_asc", "species_desc", "confidence_asc", "confidence_desc", "status", or SortBySearchDefault
	Limit         int
	Offset        int
	// MinID filters to records with ID > MinID (cursor-based pagination for migration)
	MinID uint
	// CursorPagination indicates this query uses cursor-based pagination and must
	// sort by id ASC to guarantee all records are visited.
	CursorPagination bool
}

// ConfidenceFilter represents a confidence level filter
type ConfidenceFilter struct {
	Operator string // ">", "<", ">=", "<=", "="
	Value    float64
}

// ConfidenceRangeFilter represents an inclusive confidence range. Values are
// fractions in [0, 1], matching the confidence column, not percentages.
type ConfidenceRangeFilter struct {
	Min float64
	Max float64
}

// HourFilter represents hour-based filtering
type HourFilter struct {
	Start int // 0-23
	End   int // 0-23, if same as Start then single hour
}

// DateRange represents a date range filter
type DateRange struct {
	Start time.Time
	End   time.Time
}

// SearchNotesAdvanced performs an advanced search with multiple filter support
// This is a new method that doesn't break the existing SearchNotes method
func (ds *DataStore) SearchNotesAdvanced(filters *AdvancedSearchFilters) ([]Note, int64, error) {
	// Track metrics
	// TODO: Add metrics tracking when IncrementSearches method is available
	// ds.metricsMu.RLock()
	// metrics := ds.metrics
	// ds.metricsMu.RUnlock()
	//
	// if metrics != nil {
	// 	metrics.IncrementSearches("advanced")
	// }

	// Start building the query
	query := ds.DB.Model(&Note{}).
		Preload("Review").
		Preload("Lock").
		Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC")
		})

	// Reuse the standard free-text + exact-scientific OR grouping so the simple
	// and advanced legacy search paths cannot drift.
	query = applySpeciesFilter(query, &SearchFilters{
		Species:           filters.TextQuery,
		SpeciesScientific: filters.SpeciesScientific,
	})

	// Apply confidence filter
	query = applyConfidenceFilter(query, filters.Confidence)
	query = applyConfidenceRangeFilter(query, filters.ConfidenceRange)

	// Apply date range filter
	query = applyDateRangeFilter(query, filters.DateRange)

	// Apply hour filter
	query = applyHourFilter(query, filters.Hour)

	// Apply time of day filter. Resolved against the station's real sun events,
	// bounded by the same date range the query already filters on.
	query = ds.applyTimeOfDayFilter(query, filters.TimeOfDay, filters.DateRange)

	// Apply species filter
	if len(filters.Species) > 0 {
		query = query.Where("species_code IN ? OR scientific_name IN ?", filters.Species, filters.Species)
	}

	// Apply location/source filter (source_node column in notes table)
	if len(filters.Location) > 0 {
		query = query.Where(sourceNodeInPredicate, filters.Location)
	}
	// The legacy schema records only the node per note, so a source filter can select rows only
	// by node name; any other spelling matches nothing rather than everything.
	if len(filters.Source) > 0 {
		query = query.Where(sourceNodeInPredicate, filters.Source)
	}

	// Apply verified filter
	query = applyVerifiedFilter(query, filters.VerifiedStatus, filters.Verified)

	// Apply locked filter
	query = applyLockedFilter(query, filters.Locked)

	// Apply MinID filter for cursor-based pagination (used by migration worker)
	if filters.MinID > 0 {
		query = query.Where("id > ?", filters.MinID)
	}

	// Count total results before pagination
	var totalCount int64
	countQuery := query.Session(&gorm.Session{})
	if err := countQuery.Count(&totalCount).Error; err != nil {
		return nil, 0, errors.Newf("failed to count advanced search results: %w", err).
			Context("operation", "count_advanced_search_results").
			Context("filters", fmt.Sprintf("%+v", filters)).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Build()
	}

	// When cursor-based pagination is active, MUST sort by id ASC to guarantee
	// all records are visited. Sorting by date with an id-based cursor causes records
	// to be permanently skipped when IDs don't correlate with dates (bulk imports).
	if filters.CursorPagination {
		query = query.Order("id ASC")
	} else {
		// Apply sorting based on SortBy field, falling back to SortAscending for backward compatibility
		switch strings.ToLower(filters.SortBy) {
		case SortBySearchDefault:
			query = query.Order("id DESC")
		case "date_asc":
			query = query.Order("date ASC, time ASC")
		case "species_asc":
			query = query.Order("common_name ASC")
		case "species_desc":
			query = query.Order("common_name DESC")
		case "confidence_asc":
			query = query.Order("confidence ASC")
		case "confidence_desc":
			query = query.Order("confidence DESC")
		case "status":
			// Only add note_reviews JOIN if not already joined by applyVerifiedFilter
			if !joinsVerificationTable(filters.VerifiedStatus, filters.Verified) {
				query = query.Joins("LEFT JOIN note_reviews ON note_reviews.note_id = notes.id")
			}
			query = query.Order("CASE WHEN note_reviews.verified = 'correct' THEN 0 WHEN note_reviews.verified IS NULL OR note_reviews.verified = '' THEN 1 ELSE 2 END ASC")
		default: // "date_desc" or empty - use SortAscending for backward compatibility
			if filters.SortAscending {
				query = query.Order("date ASC, time ASC")
			} else {
				query = query.Order("date DESC, time DESC")
			}
		}
	}

	// Apply pagination
	if filters.Limit > 0 {
		query = query.Limit(filters.Limit)
	}
	if filters.Offset > 0 {
		query = query.Offset(filters.Offset)
	}

	// Execute the query
	var notes []Note
	if err := query.Find(&notes).Error; err != nil {
		return nil, 0, errors.Newf("failed to execute advanced search: %w", err).
			Context("operation", "advanced_search_notes").
			Context("filters", fmt.Sprintf("%+v", filters)).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Build()
	}

	// Populate virtual fields
	for i := range notes {
		note := &notes[i]
		if note.Review != nil && note.Review.Verified != "" {
			note.Verified = note.Review.Verified
		}
		if note.Lock != nil {
			note.Locked = true
		}
	}

	return notes, totalCount, nil
}

// ParseDateShortcut converts date shortcuts like "today", "yesterday" to actual dates
func ParseDateShortcut(shortcut string) (time.Time, error) {
	now := time.Now()

	switch strings.ToLower(shortcut) {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()), nil
	case "yesterday":
		return time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location()), nil
	case "week":
		// Last 7 days
		return time.Date(now.Year(), now.Month(), now.Day()-7, 0, 0, 0, 0, now.Location()), nil
	case "month":
		// Last 30 days
		return time.Date(now.Year(), now.Month(), now.Day()-30, 0, 0, 0, 0, now.Location()), nil
	default:
		// Try parsing as a date
		return time.Parse(time.DateOnly, shortcut)
	}
}

// applyConfidenceFilter applies confidence filtering to the query
func applyConfidenceFilter(query *gorm.DB, filter *ConfidenceFilter) *gorm.DB {
	if filter == nil {
		return query
	}

	switch filter.Operator {
	case ">":
		return query.Where("confidence > ?", filter.Value)
	case "<":
		return query.Where("confidence < ?", filter.Value)
	case ">=":
		return query.Where("confidence >= ?", filter.Value)
	case "<=":
		return query.Where("confidence <= ?", filter.Value)
	case "=", ":":
		return query.Where("confidence = ?", filter.Value)
	}
	return query
}

// applyConfidenceRangeFilter applies an inclusive confidence range to the query.
// Unlike applyConfidenceFilter, which takes a single comparison, this expresses a
// band, so callers can ask for "between 60% and 80% confident".
func applyConfidenceRangeFilter(query *gorm.DB, r *ConfidenceRangeFilter) *gorm.DB {
	if r == nil {
		return query
	}
	// A zero minimum and a maximum at or above 1.0 select everything, so skip the
	// predicates rather than emitting SQL that can only narrow the plan.
	if r.Min > 0 {
		query = query.Where("confidence >= ?", r.Min)
	}
	if r.Max > 0 && r.Max < 1.0 {
		query = query.Where("confidence <= ?", r.Max)
	}
	return query
}

// applyDateRangeFilter applies date range filtering to the query
func applyDateRangeFilter(query *gorm.DB, dateRange *DateRange) *gorm.DB {
	if dateRange == nil {
		return query
	}

	startDate := dateRange.Start.Format(time.DateOnly)
	endDate := dateRange.End.Format(time.DateOnly)
	return query.Where("date >= ? AND date <= ?", startDate, endDate)
}

// applyHourFilter applies hour filtering to the query
func applyHourFilter(query *gorm.DB, hour *HourFilter) *gorm.DB {
	if hour == nil {
		return query
	}

	if hour.Start == hour.End {
		// Single hour
		hourStr := fmt.Sprintf("%02d:00:00", hour.Start)
		nextHourStr := fmt.Sprintf("%02d:00:00", (hour.Start+1)%24)
		return query.Where("time >= ? AND time < ?", hourStr, nextHourStr)
	}

	// Hour range
	startHourStr := fmt.Sprintf("%02d:00:00", hour.Start)
	endHourStr := fmt.Sprintf("%02d:00:00", hour.End+1) // Include the end hour

	if hour.Start < hour.End {
		// Normal range (e.g., 6-9)
		return query.Where("time >= ? AND time < ?", startHourStr, endHourStr)
	}

	// Wraps around midnight (e.g., 22-2)
	return query.Where("(time >= ? OR time < ?)", startHourStr, endHourStr)
}

// NormalizeTimeOfDayPeriods maps a caller-supplied time-of-day period list onto
// the canonical vocabulary used by the simple search path: "day", "night",
// "sunrise", "sunset".
//
// The advanced path historically spoke "dawn"/"dusk" and meant fixed clock
// windows, while the simple path spoke "sunrise"/"sunset" and meant real sun
// events. Both now resolve against sun events, so the two legacy names are
// accepted as aliases rather than being a separate, differently-behaving filter.
// "any" and unrecognized values are dropped (an empty result means "no filter"),
// and duplicates are collapsed so an aliased list cannot apply a period twice.
func NormalizeTimeOfDayPeriods(periods []string) []string {
	if len(periods) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(periods))
	seen := make(map[string]struct{}, len(periods))
	for _, p := range periods {
		var canonical string
		switch strings.ToLower(strings.TrimSpace(p)) {
		case TimeOfDayDay:
			canonical = TimeOfDayDay
		case TimeOfDayNight:
			canonical = TimeOfDayNight
		case TimeOfDaySunrise, "dawn":
			canonical = TimeOfDaySunrise
		case TimeOfDaySunset, "dusk":
			canonical = TimeOfDaySunset
		default:
			// "", "any" and anything unrecognized: no constraint.
			continue
		}
		if _, dup := seen[canonical]; dup {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// applyTimeOfDayFilter applies time-of-day filtering to an advanced-search query.
//
// It resolves each requested period against the station's real sun events, using
// the same per-date condition builder as the simple search path, so the two
// endpoints cannot disagree about what "sunset" means. Multiple periods are OR-ed.
// When sun events are unavailable (no SunCalc configured, i.e. no station
// location), it falls back to the historical fixed clock windows rather than
// dropping the filter and silently returning everything.
func (ds *DataStore) applyTimeOfDayFilter(query *gorm.DB, periods []string, dateRange *DateRange) *gorm.DB {
	periods = NormalizeTimeOfDayPeriods(periods)
	if len(periods) == 0 {
		return query
	}

	if ds.SunCalc == nil || ds.DB == nil {
		return applyTimeOfDayClockWindows(query, periods)
	}

	// buildTimeOfDayConditions needs a bounded date range: it emits one condition
	// per calendar date and refuses ranges longer than a year. An unfiltered query
	// has no range, in which case it defaults to the last year itself.
	var startDateStr, endDateStr string
	if dateRange != nil {
		startDateStr = dateRange.Start.Format(time.DateOnly)
		endDateStr = dateRange.End.Format(time.DateOnly)
	}

	var combined *gorm.DB
	for _, period := range periods {
		dateConditions, err := buildTimeOfDayConditions(period, startDateStr, endDateStr, ds.SunCalc, ds.DB)
		if err != nil {
			GetLogger().Warn("Failed to build advanced TimeOfDay conditions, falling back to clock windows",
				logger.String("period", period),
				logger.Error(err))
			return applyTimeOfDayClockWindows(query, periods)
		}
		for _, cond := range dateConditions {
			if combined == nil {
				combined = ds.DB.Where(cond)
				continue
			}
			combined = combined.Or(cond)
		}
	}

	if combined == nil {
		// No sun events could be resolved for any date in range; leaving the query
		// unfiltered would return every row, so match nothing instead — the same
		// outcome the caller asked for when no detection falls in the period.
		return query.Where("1 = 0")
	}
	return query.Where(combined)
}

// applyTimeOfDayClockWindows is the sun-event-free fallback: fixed local clock
// windows approximating each period. Used only when no station location is
// configured, so SunCalc cannot resolve real sunrise/sunset times.
func applyTimeOfDayClockWindows(query *gorm.DB, periods []string) *gorm.DB {
	var timeConditions []string
	var args []any

	for _, tod := range periods {
		switch tod {
		case TimeOfDaySunrise:
			// Approximate the sunrise window as 5-7 AM.
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "05:00:00", "07:00:00")
		case TimeOfDayDay:
			// Approximate day as 7 AM - 6 PM.
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "07:00:00", "18:00:00")
		case TimeOfDaySunset:
			// Approximate the sunset window as 6-8 PM.
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "18:00:00", "20:00:00")
		case TimeOfDayNight:
			// Approximate night as 8 PM - 5 AM.
			timeConditions = append(timeConditions, "(time >= ? OR time < ?)")
			args = append(args, "20:00:00", "05:00:00")
		}
	}

	if len(timeConditions) > 0 {
		return query.Where("("+strings.Join(timeConditions, " OR ")+")", args...)
	}

	return query
}

// applyVerifiedFilter applies verified filtering to the query.
//
// status is the three-state verdict filter and takes precedence; verified is the
// legacy two-state fallback used when no status is given. Passing "correct" or
// "false_positive" selects that verdict specifically, which the boolean form
// cannot express.
func applyVerifiedFilter(query *gorm.DB, status string, verified *bool) *gorm.DB {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case VerifiedStatusCorrect:
		return query.Joins("INNER JOIN note_reviews ON note_reviews.note_id = notes.id").
			Where("note_reviews.verified = ?", VerifiedStatusCorrect)
	case VerifiedStatusFalsePositive:
		return query.Joins("INNER JOIN note_reviews ON note_reviews.note_id = notes.id").
			Where("note_reviews.verified = ?", VerifiedStatusFalsePositive)
	case VerifiedStatusUnverified:
		return query.Joins("LEFT JOIN note_reviews ON note_reviews.note_id = notes.id").
			Where("note_reviews.id IS NULL OR note_reviews.verified = ''")
	}

	// No explicit status: fall back to the legacy reviewed/not-reviewed boolean.
	if verified == nil {
		return query
	}

	if *verified {
		return query.Joins("INNER JOIN note_reviews ON note_reviews.note_id = notes.id AND note_reviews.verified != ''")
	}

	return query.Joins("LEFT JOIN note_reviews ON note_reviews.note_id = notes.id").
		Where("note_reviews.id IS NULL OR note_reviews.verified = ''")
}

// joinsVerificationTable reports whether the given verification filters cause
// applyVerifiedFilter to join note_reviews. The "status" sort orders on that
// table and must add its own LEFT JOIN when the filter did not, but must not
// join twice when it did.
func joinsVerificationTable(status string, verified *bool) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case VerifiedStatusCorrect, VerifiedStatusFalsePositive, VerifiedStatusUnverified:
		return true
	}
	return verified != nil
}

// applyLockedFilter applies locked filtering to the query
func applyLockedFilter(query *gorm.DB, locked *bool) *gorm.DB {
	if locked == nil {
		return query
	}

	if *locked {
		return query.Joins("INNER JOIN note_locks ON note_locks.note_id = notes.id")
	}

	return query.Joins("LEFT JOIN note_locks ON note_locks.note_id = notes.id").
		Where("note_locks.id IS NULL")
}
