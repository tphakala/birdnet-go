// Package datastore provides helper functions for logging and metrics
package datastore

import (
	"regexp"
	"strings"
	"time"
)

// sqlUnknown is used when SQL operation or table cannot be determined.
const sqlUnknown = "unknown"

// SQL operation regex patterns
var (
	selectPattern = regexp.MustCompile(`(?i)^\s*SELECT\s+.*?\s+FROM\s+['"\x60]?(\w+)['"\x60]?`)
	insertPattern = regexp.MustCompile(`(?i)^\s*INSERT\s+INTO\s+['"\x60]?(\w+)['"\x60]?`)
	updatePattern = regexp.MustCompile(`(?i)^\s*UPDATE\s+['"\x60]?(\w+)['"\x60]?`)
	deletePattern = regexp.MustCompile(`(?i)^\s*DELETE\s+FROM\s+['"\x60]?(\w+)['"\x60]?`)
	createPattern = regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?['"\x60]?(\w+)['"\x60]?`)
	dropPattern   = regexp.MustCompile(`(?i)^\s*DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?['"\x60]?(\w+)['"\x60]?`)
	alterPattern  = regexp.MustCompile(`(?i)^\s*ALTER\s+TABLE\s+['"\x60]?(\w+)['"\x60]?`)
)

// parseSQLOperation extracts the operation type and table name from SQL query
func parseSQLOperation(sql string) (operation, table string) {
	sql = strings.TrimSpace(sql)

	// Try to match against known patterns
	if matches := selectPattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "select", matches[1]
	}
	if matches := insertPattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "insert", matches[1]
	}
	if matches := updatePattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "update", matches[1]
	}
	if matches := deletePattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "delete", matches[1]
	}
	if matches := createPattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "create", matches[1]
	}
	if matches := dropPattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "drop", matches[1]
	}
	if matches := alterPattern.FindStringSubmatch(sql); len(matches) > 1 {
		return "alter", matches[1]
	}

	// Default for unrecognized patterns
	return sqlUnknown, sqlUnknown
}

// categorizeError categorizes database errors for metrics
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}

	// First, try to categorize based on known error types
	// Check for PostgreSQL-specific errors using type assertions
	// Note: pgconn.PgError would be used if this was a PostgreSQL setup
	// For now, keeping the interface open for future database-specific error handling

	// Convert to string for pattern matching
	errStr := strings.ToLower(err.Error())

	switch {
	case strings.Contains(errStr, "unique constraint") || strings.Contains(errStr, "duplicate key"):
		return "constraint_violation"
	case strings.Contains(errStr, "deadlock"):
		return "deadlock"
	case strings.Contains(errStr, "foreign key"):
		return "foreign_key_violation"
	case strings.Contains(errStr, "not null"):
		return "null_violation"
	case strings.Contains(errStr, "database is locked"):
		return "database_locked"
	case strings.Contains(errStr, "connection"):
		return "connection_error"
	case strings.Contains(errStr, "timeout"):
		return "timeout"
	case strings.Contains(errStr, "syntax"):
		return "syntax_error"
	case strings.Contains(errStr, "permission") || strings.Contains(errStr, "denied"):
		return "permission_denied"
	case strings.Contains(errStr, "disk full") || strings.Contains(errStr, "no space"):
		return "disk_full"
	default:
		return "other"
	}
}

// calculateFilterComplexity calculates complexity score for search filters
func calculateFilterComplexity(filters *SearchFilters) float64 {
	if filters == nil {
		return 0
	}

	complexity := 0.0

	// Add complexity for each active filter
	if filters.Species != "" {
		complexity += 1
	}
	if filters.DateStart != "" {
		complexity += 1
	}
	if filters.DateEnd != "" {
		complexity += 1
	}
	if filters.ConfidenceMin > 0 {
		complexity += 0.5
	}
	if filters.ConfidenceMax > 0 && filters.ConfidenceMax < 1.0 {
		complexity += 0.5
	}
	if filters.VerifiedOnly {
		complexity += 1
	}
	if filters.UnverifiedOnly {
		complexity += 1
	}
	if filters.LockedOnly {
		complexity += 1
	}
	if filters.UnlockedOnly {
		complexity += 1
	}
	if filters.Device != "" {
		complexity += 1
	}
	if filters.TimeOfDay != "" && filters.TimeOfDay != "any" {
		complexity += 2 // Time-based filters are more complex
	}

	return complexity
}

// calculateDateRangeComplexity calculates complexity based on date range
func calculateDateRangeComplexity(startDate, endDate string) float64 {
	// Return default complexity if either date is empty
	if startDate == "" || endDate == "" {
		return 1.0
	}

	// Parse start date
	start, err := time.Parse(time.DateOnly, startDate)
	if err != nil {
		// Try alternative date format
		// IMPORTANT: Database stores local time strings, parse as local time
		start, err = time.ParseInLocation("2006-01-02 15:04:05", startDate, time.Local)
		if err != nil {
			// Return default complexity if parsing fails
			return 1.0
		}
	}

	// Parse end date
	end, err := time.Parse(time.DateOnly, endDate)
	if err != nil {
		// Try alternative date format
		// IMPORTANT: Database stores local time strings, parse as local time
		end, err = time.ParseInLocation("2006-01-02 15:04:05", endDate, time.Local)
		if err != nil {
			// Return default complexity if parsing fails
			return 1.0
		}
	}

	// Calculate the difference in days
	daysDiff := end.Sub(start).Hours() / 24
	if daysDiff < 0 {
		daysDiff = -daysDiff // Take absolute value
	}

	// Calculate complexity based on range length
	switch {
	case daysDiff <= 1:
		return 1.0 // Single day
	case daysDiff <= 7:
		return 2.0 // Week
	case daysDiff <= 30:
		return 3.0 // Month
	case daysDiff <= 90:
		return 4.0 // Quarter
	case daysDiff <= 365:
		return 5.0 // Year
	default:
		return 6.0 // More than a year
	}
}

// isConstraintViolation checks if an error is a unique constraint violation
// in a database-agnostic way using the categorizeError helper
func isConstraintViolation(err error) bool {
	return categorizeError(err) == "constraint_violation"
}

// getAppliedFilters returns a summary of applied filters for logging
func getAppliedFilters(filters *SearchFilters) map[string]any {
	if filters == nil {
		return map[string]any{"filters": "none"}
	}

	applied := make(map[string]any)

	if filters.Species != "" {
		applied["species"] = filters.Species
	}
	if filters.DateStart != "" {
		applied["date_start"] = filters.DateStart
	}
	if filters.DateEnd != "" {
		applied["date_end"] = filters.DateEnd
	}
	if filters.ConfidenceMin > 0 {
		applied["confidence_min"] = filters.ConfidenceMin
	}
	if filters.ConfidenceMax > 0 && filters.ConfidenceMax < 1.0 {
		applied["confidence_max"] = filters.ConfidenceMax
	}
	if filters.VerifiedOnly {
		applied["verified_only"] = filters.VerifiedOnly
	}
	if filters.UnverifiedOnly {
		applied["unverified_only"] = filters.UnverifiedOnly
	}
	if filters.LockedOnly {
		applied["locked_only"] = filters.LockedOnly
	}
	if filters.UnlockedOnly {
		applied["unlocked_only"] = filters.UnlockedOnly
	}
	if filters.Device != "" {
		applied["device"] = filters.Device
	}
	if filters.TimeOfDay != "" && filters.TimeOfDay != "any" {
		applied["time_of_day"] = filters.TimeOfDay
	}

	return applied
}
