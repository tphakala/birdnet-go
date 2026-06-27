// query_filters.go holds shared query-parameter parsing helpers used by more than
// one api/v2 domain (the detections/search handlers consume them at request time;
// the package-api fuzz and analytics code unit-test and reuse them). They live on
// the shared substrate so the detections domain package and the facade reference a
// single source without importing each other.
package apicore

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// PercentageMultiplier converts a fraction to a percentage (and divides a
// percentage back to a fraction). It is the canonical single source shared by the
// confidence-filter parsing here and the analytics percentage parsing in package
// api (which aliases this constant).
const PercentageMultiplier = 100.0

// minHourRangeParts is the number of parts a valid "start-end" hour range splits
// into. Used by ParseHourFilter.
const minHourRangeParts = 2

// ConfidenceFilterResult holds the parsed confidence filter parameters.
type ConfidenceFilterResult struct {
	Operator string
	Value    float64
}

// ParseConfidenceFilter parses a confidence filter parameter with operator support.
// Supports operators: >=, <=, >, <, = (default)
// Returns nil if the parameter is empty.
func ParseConfidenceFilter(param string) *ConfidenceFilterResult {
	if param == "" {
		return nil
	}

	var operator string
	var value string

	switch {
	case strings.HasPrefix(param, ">="):
		operator = ">="
		value = param[2:]
	case strings.HasPrefix(param, "<="):
		operator = "<="
		value = param[2:]
	case strings.HasPrefix(param, ">"):
		operator = ">"
		value = param[1:]
	case strings.HasPrefix(param, "<"):
		operator = "<"
		value = param[1:]
	default:
		operator = "="
		value = param
	}

	confValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}

	// Validate confidence is within 0-100 range and not NaN
	if math.IsNaN(confValue) || confValue < 0 || confValue > 100 {
		return nil
	}

	return &ConfidenceFilterResult{
		Operator: operator,
		Value:    confValue / PercentageMultiplier,
	}
}

// HourFilterResult holds the parsed hour filter parameters.
type HourFilterResult struct {
	Start int
	End   int
}

// ParseHourFilter parses an hour filter parameter.
// Supports single hour ("6") or range format ("6-9").
// Returns nil if the parameter is empty, invalid, out of range (0-23), or has inverted range.
func ParseHourFilter(param string) *HourFilterResult {
	if param == "" {
		return nil
	}

	if strings.Contains(param, "-") {
		// Range format: "6-9"
		parts := strings.Split(param, "-")
		if len(parts) != minHourRangeParts {
			return nil
		}
		start, err1 := strconv.Atoi(parts[0])
		end, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return nil
		}
		// Validate hour range (0-23) and ensure start <= end
		if start < 0 || start > 23 || end < 0 || end > 23 || start > end {
			return nil
		}
		return &HourFilterResult{Start: start, End: end}
	}

	// Single hour
	hourVal, err := strconv.Atoi(param)
	if err != nil {
		return nil
	}
	// Validate hour is within 0-23
	if hourVal < 0 || hourVal > 23 {
		return nil
	}
	return &HourFilterResult{Start: hourVal, End: hourVal}
}

// DateRangeResult holds the parsed date range.
type DateRangeResult struct {
	Start time.Time
	End   time.Time
}

// ParseDateRangeFilter parses date range from single date or start/end date parameters.
// If singleDate is provided, it's used as both start and end.
// Returns nil if no valid dates are provided.
func ParseDateRangeFilter(singleDate, startDate, endDate string) *DateRangeResult {
	if singleDate != "" {
		// Try date shortcuts first (today, yesterday, etc.)
		if date, err := datastore.ParseDateShortcut(singleDate); err == nil {
			return &DateRangeResult{
				Start: date,
				End:   date.AddDate(0, 0, 1).Add(-time.Second),
			}
		}
	}

	if startDate != "" && endDate != "" {
		start, err1 := time.Parse(time.DateOnly, startDate)
		end, err2 := time.Parse(time.DateOnly, endDate)
		if err1 == nil && err2 == nil {
			// Reject inverted date ranges where start is after end
			if start.After(end) {
				return nil
			}
			return &DateRangeResult{
				Start: start,
				End:   end.AddDate(0, 0, 1).Add(-time.Second),
			}
		}
	}

	return nil
}
