// Package datastore provides helper functions for logging and metrics
package datastore

import (
	"regexp"
	"strings"
)

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
	return "unknown", "unknown"
}

// categorizeError categorizes database errors for metrics
func categorizeError(err error) string {
	if err == nil {
		return "none"
	}
	
	errStr := strings.ToLower(err.Error())
	
	switch {
	case strings.Contains(errStr, "unique constraint"):
		return "constraint_violation"
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
	// Simple implementation - could be enhanced to calculate actual date difference
	if startDate == "" || endDate == "" {
		return 1
	}
	
	// For now, just return a fixed complexity
	// In a real implementation, we'd parse dates and calculate the range
	return 5
}

// getAppliedFilters returns a summary of applied filters for logging
func getAppliedFilters(filters *SearchFilters) map[string]interface{} {
	if filters == nil {
		return map[string]interface{}{"filters": "none"}
	}
	
	applied := make(map[string]interface{})
	
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