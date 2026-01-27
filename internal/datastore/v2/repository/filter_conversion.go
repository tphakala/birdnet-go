// Package repository provides filter conversion helpers for mapping
// API-level filters to database-level filters.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// =============================================================================
// Time Period Constants
// =============================================================================

// Time period hour boundaries for filtering.
// These define the hour ranges for each time-of-day period.
const (
	// DawnStartHour is the beginning of dawn (5:00 AM).
	DawnStartHour = 5
	// DawnEndHour is the end of dawn (6:59 AM).
	DawnEndHour = 6

	// DayStartHour is the beginning of day (7:00 AM).
	DayStartHour = 7
	// DayEndHour is the end of day (5:59 PM).
	DayEndHour = 17

	// DuskStartHour is the beginning of dusk (6:00 PM).
	DuskStartHour = 18
	// DuskEndHour is the end of dusk (7:59 PM).
	DuskEndHour = 19

	// NightStartHour is the beginning of night (8:00 PM).
	NightStartHour = 20
	// NightEndHour is the end of night (4:59 AM, wraps midnight).
	NightEndHour = 4
)

// =============================================================================
// Time Conversion Helpers
// =============================================================================

// DateRangeToUnix converts a DateRange to Unix timestamps.
// Returns start of first day (00:00:00) and end of last day (23:59:59).
// Returns nil, nil if dr is nil.
func DateRangeToUnix(dr *datastore.DateRange, tz *time.Location) (start, end *int64) {
	if dr == nil {
		return nil, nil
	}
	if tz == nil {
		tz = time.Local
	}

	// Start of first day (00:00:00)
	startTime := time.Date(
		dr.Start.Year(), dr.Start.Month(), dr.Start.Day(),
		0, 0, 0, 0, tz,
	)
	s := startTime.Unix()

	// End of last day (23:59:59)
	endTime := time.Date(
		dr.End.Year(), dr.End.Month(), dr.End.Day(),
		23, 59, 59, 0, tz,
	)
	e := endTime.Unix()

	return &s, &e
}

// TimeOfDayToHours converts time-of-day period strings to a list of hours.
// Supported periods:
//   - "dawn": hours 5, 6 (5:00 AM - 6:59 AM)
//   - "day": hours 7-17 (7:00 AM - 5:59 PM)
//   - "dusk": hours 18, 19 (6:00 PM - 7:59 PM)
//   - "night": hours 20-23, 0-4 (8:00 PM - 4:59 AM)
//
// Returns nil if periods is empty.
func TimeOfDayToHours(periods []string) []int {
	if len(periods) == 0 {
		return nil
	}

	// Use a map to deduplicate hours
	hourSet := make(map[int]struct{})

	for _, period := range periods {
		switch strings.ToLower(period) {
		case "dawn":
			for h := DawnStartHour; h <= DawnEndHour; h++ {
				hourSet[h] = struct{}{}
			}
		case "day":
			for h := DayStartHour; h <= DayEndHour; h++ {
				hourSet[h] = struct{}{}
			}
		case "dusk":
			for h := DuskStartHour; h <= DuskEndHour; h++ {
				hourSet[h] = struct{}{}
			}
		case "night":
			// Night wraps around midnight
			for h := NightStartHour; h <= 23; h++ {
				hourSet[h] = struct{}{}
			}
			for h := 0; h <= NightEndHour; h++ {
				hourSet[h] = struct{}{}
			}
		}
	}

	if len(hourSet) == 0 {
		return nil
	}

	// Convert map to slice
	hours := make([]int, 0, len(hourSet))
	for h := range hourSet {
		hours = append(hours, h)
	}

	return hours
}

// HourFilterToHours converts an HourFilter to a list of hours.
// Handles wrap-around (e.g., Start=22, End=2 → [22, 23, 0, 1, 2]).
// Returns nil if hf is nil.
func HourFilterToHours(hf *datastore.HourFilter) []int {
	if hf == nil {
		return nil
	}

	// Normalize to valid hour range
	start := hf.Start % 24
	end := hf.End % 24

	hours := make([]int, 0)

	if start <= end {
		// Normal range (e.g., 6-18)
		for h := start; h <= end; h++ {
			hours = append(hours, h)
		}
	} else {
		// Wraps around midnight (e.g., 22-2)
		for h := start; h <= 23; h++ {
			hours = append(hours, h)
		}
		for h := 0; h <= end; h++ {
			hours = append(hours, h)
		}
	}

	return hours
}

// MergeHourFilters combines TimeOfDay and Hour filters.
// If both are provided, returns the intersection.
// If only one is provided, returns that one.
// If neither is provided, returns nil (no hour filtering).
func MergeHourFilters(timeOfDay []string, hour *datastore.HourFilter) []int {
	todHours := TimeOfDayToHours(timeOfDay)
	hourFilterHours := HourFilterToHours(hour)

	if todHours == nil && hourFilterHours == nil {
		return nil
	}
	if todHours == nil {
		return hourFilterHours
	}
	if hourFilterHours == nil {
		return todHours
	}

	// Return intersection
	todSet := make(map[int]struct{}, len(todHours))
	for _, h := range todHours {
		todSet[h] = struct{}{}
	}

	intersection := make([]int, 0)
	for _, h := range hourFilterHours {
		if _, exists := todSet[h]; exists {
			intersection = append(intersection, h)
		}
	}

	// If intersection is empty but both filters were provided,
	// return sentinel to ensure zero results
	if len(intersection) == 0 {
		return []int{-1} // Invalid hour, will match nothing
	}

	return intersection
}

// GetTimezoneOffset returns the timezone offset in seconds for the given location.
// Uses the current time to determine the offset (handles DST).
//
// LIMITATION: This applies the current DST state to all data. When filtering
// historical data that spans DST transitions, hour boundaries may be off by
// up to 1 hour for data recorded in a different DST state than the current one.
// For most use cases (recent data, non-DST timezones), this is acceptable.
// For precise historical hour filtering across DST boundaries, consider using
// database-native timezone conversion functions.
func GetTimezoneOffset(tz *time.Location) int {
	if tz == nil {
		tz = time.Local
	}
	_, offset := time.Now().In(tz).Zone()
	return offset
}

// =============================================================================
// Confidence Conversion Helpers
// =============================================================================

// ConfidenceFilterToMinMax converts a ConfidenceFilter to min/max values.
// Handles operators: ">", ">=", "<", "<=", "=", ":"
// Returns nil, nil if cf is nil.
func ConfidenceFilterToMinMax(cf *datastore.ConfidenceFilter) (minConf, maxConf *float64) {
	if cf == nil {
		return nil, nil
	}

	// Always return pointer to local copy to prevent unintended mutation of the original struct.
	switch cf.Operator {
case ">":
		// Greater than
		v := cf.Value
		return &v, nil
	case ">=":
		v := cf.Value
		return &v, nil
case "<":
		// Less than
		v := cf.Value
		return nil, &v
	case "<=":
		v := cf.Value
		return nil, &v
	case "=", ":":
		// Exact match - set both min and max
		v := cf.Value
		return &v, &v
	}

	return nil, nil
}

// =============================================================================
// Entity Lookup Helpers
// =============================================================================

// sentinelNoMatchIDs is returned when filter input is non-empty but resolves
// to no matching entities. ID 0 never exists in the database, so this ensures
// queries return zero results. This distinguishes "filter to nothing" from
// "no filter applied" (nil).
var sentinelNoMatchIDs = []uint{0}

// FilterLookupDeps contains dependencies for filter entity lookups.
type FilterLookupDeps struct {
	LabelRepo  LabelRepository
	SourceRepo AudioSourceRepository
}

// ResolveSpeciesToLabelIDs converts species names to label IDs.
// Accepts scientific names (looked up via GetByScientificName).
// If species is non-empty but no labels are found, returns sentinel []uint{0}
// to ensure the query returns zero results (rather than ignoring the filter).
// Returns nil if species is empty.
func ResolveSpeciesToLabelIDs(ctx context.Context, deps *FilterLookupDeps, species []string) ([]uint, error) {
	if len(species) == 0 {
		return nil, nil
	}
	if deps == nil || deps.LabelRepo == nil {
		return nil, nil
	}

	labelIDs := make([]uint, 0, len(species))
	for _, name := range species {
		label, err := deps.LabelRepo.GetByScientificName(ctx, name)
		if err != nil {
			if errors.Is(err, ErrLabelNotFound) {
				continue // Skip unknown species
			}
			return nil, err
		}
		labelIDs = append(labelIDs, label.ID)
	}

	// If input was non-empty but we found nothing, use sentinel
	if len(labelIDs) == 0 {
		return sentinelNoMatchIDs, nil
	}

	return labelIDs, nil
}

// ResolveLocationsToSourceIDs converts location/node names to audio source IDs.
// If locations is non-empty but no sources are found, returns sentinel []uint{0}
// to ensure the query returns zero results.
// Returns nil if locations is empty.
func ResolveLocationsToSourceIDs(ctx context.Context, deps *FilterLookupDeps, locations []string) ([]uint, error) {
	if len(locations) == 0 {
		return nil, nil
	}
	if deps == nil || deps.SourceRepo == nil {
		return nil, nil
	}

	sourceIDs := make([]uint, 0, len(locations))
	for _, nodeName := range locations {
		sources, err := deps.SourceRepo.GetByNodeName(ctx, nodeName)
		if err != nil {
			return nil, err
		}
		for _, src := range sources {
			sourceIDs = append(sourceIDs, src.ID)
		}
	}

	// If input was non-empty but we found nothing, use sentinel
	if len(sourceIDs) == 0 {
		return sentinelNoMatchIDs, nil
	}

	return sourceIDs, nil
}

// =============================================================================
// Main Conversion Function
// =============================================================================

// ConvertAdvancedFilters converts AdvancedSearchFilters to repository SearchFilters.
// This is the main entry point for filter conversion.
//
// Parameters:
//   - ctx: Context for database lookups
//   - filters: The API-level filters to convert
//   - deps: Repository dependencies for entity lookups (can be nil for direct field mappings only)
//   - tz: Timezone for date/hour calculations (defaults to Local if nil)
func ConvertAdvancedFilters(
	ctx context.Context,
	filters *datastore.AdvancedSearchFilters,
	deps *FilterLookupDeps,
	tz *time.Location,
) (*SearchFilters, error) {
	if filters == nil {
		return &SearchFilters{}, nil
	}
	if tz == nil {
		tz = time.Local
	}

	sf := &SearchFilters{
		// Direct mappings
		Query:    filters.TextQuery,
		IsLocked: filters.Locked,
		Limit:    filters.Limit,
		Offset:   filters.Offset,
		MinID:    filters.MinID,

		// Sort
		SortBy:   SortFieldDetectedAt,
		SortDesc: !filters.SortAscending,

		// Timezone for hour calculations
		TimezoneOffset: GetTimezoneOffset(tz),
	}

	// Time conversions
	sf.StartTime, sf.EndTime = DateRangeToUnix(filters.DateRange, tz)

	// Hour filtering (merge TimeOfDay and Hour filters)
	sf.IncludedHours = MergeHourFilters(filters.TimeOfDay, filters.Hour)

	// Confidence conversion
	sf.MinConfidence, sf.MaxConfidence = ConfidenceFilterToMinMax(filters.Confidence)

	// Verified → IsReviewed conversion
	// In AdvancedSearchFilters, Verified is a bool:
	//   true = show only verified/reviewed detections
	//   false = show only unverified/unreviewed detections
	if filters.Verified != nil {
		sf.IsReviewed = filters.Verified
	}

	// Entity lookups (require deps)
	if deps != nil {
		var err error

		// Convert species names to label IDs
		sf.LabelIDs, err = ResolveSpeciesToLabelIDs(ctx, deps, filters.Species)
		if err != nil {
			return nil, err
		}

		// Convert location names to audio source IDs
		sf.AudioSourceIDs, err = ResolveLocationsToSourceIDs(ctx, deps, filters.Location)
		if err != nil {
			return nil, err
		}
	}

	return sf, nil
}
