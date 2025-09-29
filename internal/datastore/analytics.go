// internal/datastore/analytics.go
package datastore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// isDebugLoggingEnabled returns true if debug logging is enabled and logger is available
func isDebugLoggingEnabled() bool {
	settings := conf.GetSettings()
	return settings != nil && settings.Debug && datastoreLogger != nil
}

// SpeciesSummaryData contains overall statistics for a bird species
type SpeciesSummaryData struct {
	ScientificName string
	CommonName     string
	SpeciesCode    string
	Count          int
	FirstSeen      time.Time
	LastSeen       time.Time
	AvgConfidence  float64
	MaxConfidence  float64
}

// HourlyAnalyticsData represents detection counts by hour
type HourlyAnalyticsData struct {
	Hour  int
	Count int
}

// DailyAnalyticsData represents detection counts by day
type DailyAnalyticsData struct {
	Date  string
	Count int
}

// HourlyDistributionData represents aggregated detection counts by hour of day
type HourlyDistributionData struct {
	Hour  int    `json:"hour"`
	Count int    `json:"count"`
	Date  string `json:"date,omitempty"` // Optional field, only set when filtering by specific date
}

// NewSpeciesData represents a species detected for the first time within a period
type NewSpeciesData struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	FirstSeenDate  string `json:"first_seen_date"` // The absolute first date
	CountInPeriod  int    `json:"count_in_period"` // Optional: How many times seen in the query period
}

// GetSpeciesSummaryData retrieves overall statistics for all bird species
// Optional date range filtering with startDate and endDate parameters in YYYY-MM-DD format
func (ds *DataStore) GetSpeciesSummaryData(startDate, endDate string) ([]SpeciesSummaryData, error) {
	// Pre-allocate with reasonable capacity for typical species count
	summaries := make([]SpeciesSummaryData, 0, 100)

	// Track query time for performance monitoring
	queryStart := time.Now()

	// Debug logging for query start
	if isDebugLoggingEnabled() {
		getLogger().Debug("GetSpeciesSummaryData: Starting query",
			"start_date", startDate,
			"end_date", endDate)
	}

	// Get database-specific datetime formatting
	// TODO: Consider using GetDateTimeExpr("notes.date", "notes.time") for future JOIN support
	dateTimeFormat := ds.GetDateTimeFormat()
	if dateTimeFormat == "" {
		// Safely get database type for error context
		dialectName := "unknown"
		if d := ds.Dialector(); d != nil {
			dialectName = d.Name()
		}
		return nil, errors.Newf("unsupported database type for datetime formatting").
			Component("datastore").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_species_summary_data").
			Context("database_type", dialectName).
			Build()
	}

	// Start building query
	queryStr := fmt.Sprintf(`
		SELECT
			scientific_name,
			MAX(common_name) as common_name,
			MAX(species_code) as species_code,
			COUNT(*) as count,
			MIN(%s) as first_seen,
			MAX(%s) as last_seen,
			AVG(confidence) as avg_confidence,
			MAX(confidence) as max_confidence
		FROM notes
	`, dateTimeFormat, dateTimeFormat)

	// Add WHERE clause if date filters are provided
	var whereClause string
	var args []any

	switch {
	case startDate != "" && endDate != "":
		whereClause = "WHERE date >= ? AND date <= ?"
		args = append(args, startDate, endDate)
	case startDate != "":
		whereClause = "WHERE date >= ?"
		args = append(args, startDate)
	case endDate != "":
		whereClause = "WHERE date <= ?"
		args = append(args, endDate)
	}

	// Complete the query
	queryStr += whereClause + `
		GROUP BY scientific_name
		ORDER BY count DESC
	`

	// Execute the query
	if isDebugLoggingEnabled() {
		getLogger().Debug("GetSpeciesSummaryData: Executing query",
			"query", queryStr,
			"args", args)
	}
	rows, err := ds.DB.Raw(queryStr, args...).Rows()
	if err != nil {
		return nil, dbError(err, "get_species_summary_data", errors.PriorityMedium,
			"start_date", startDate,
			"end_date", endDate,
			"action", "generate_species_analytics_report")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			getLogger().Error("Failed to close rows",
				"error", err,
				"operation", "get_species_summary_data")
		}
	}()

	queryExecutionTime := time.Since(queryStart)
	if isDebugLoggingEnabled() {
		getLogger().Debug("GetSpeciesSummaryData: Query executed, scanning rows",
			"query_duration_ms", queryExecutionTime.Milliseconds())
	}
	rowCount := 0

	for rows.Next() {
		rowCount++
		var summary SpeciesSummaryData
		var firstSeenStr, lastSeenStr string
		var commonName, speciesCode sql.NullString

		if err := rows.Scan(
			&summary.ScientificName,
			&commonName,
			&speciesCode,
			&summary.Count,
			&firstSeenStr,
			&lastSeenStr,
			&summary.AvgConfidence,
			&summary.MaxConfidence,
		); err != nil {
			return nil, dbError(err, "scan_species_summary_data", errors.PriorityLow,
				"action", "parse_analytics_query_results")
		}

		// Convert nullable strings to regular strings
		summary.CommonName = commonName.String
		summary.SpeciesCode = speciesCode.String

		// Parse time strings to time.Time
		// IMPORTANT: Database stores local time strings, parse as local time
		if firstSeenStr != "" {
			firstSeen, err := time.ParseInLocation("2006-01-02 15:04:05", firstSeenStr, time.Local)
			if err == nil {
				summary.FirstSeen = firstSeen
			} else if isDebugLoggingEnabled() {
				datastoreLogger.Debug("Failed to parse firstSeen time", 
					"species", summary.ScientificName,
					"firstSeenStr", firstSeenStr,
					"error", err)
			}
		}

		if lastSeenStr != "" {
			lastSeen, err := time.ParseInLocation("2006-01-02 15:04:05", lastSeenStr, time.Local)
			if err == nil {
				summary.LastSeen = lastSeen
			} else if isDebugLoggingEnabled() {
				datastoreLogger.Debug("Failed to parse lastSeen time", 
					"species", summary.ScientificName,
					"lastSeenStr", lastSeenStr,
					"error", err)
			}
		}

		summaries = append(summaries, summary)
	}

	totalDuration := time.Since(queryStart)
	if isDebugLoggingEnabled() {
		getLogger().Debug("GetSpeciesSummaryData: Completed",
			"total_duration_ms", totalDuration.Milliseconds(),
			"rows_processed", rowCount)
	}

	return summaries, nil
}

// GetHourlyAnalyticsData retrieves detection counts grouped by hour
func (ds *DataStore) GetHourlyAnalyticsData(date, species string) ([]HourlyAnalyticsData, error) {
	var analytics []HourlyAnalyticsData
	hourFormat := ds.GetHourFormat()

	// Base query
	query := ds.DB.Table("notes").
		Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
		Group(hourFormat).
		Order("hour")

	// Apply filters
	if date != "" {
		query = query.Where("date = ?", date)
	}

	if species != "" {
		query = query.Where("scientific_name = ? OR common_name = ?", species, species)
	}

	// Execute query
	if err := query.Scan(&analytics).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_analytics_data").
			Context("date", date).
			Context("species", species).
			Build()
	}

	return analytics, nil
}

// GetDailyAnalyticsData retrieves detection counts grouped by day
func (ds *DataStore) GetDailyAnalyticsData(startDate, endDate, species string) ([]DailyAnalyticsData, error) {
	var analytics []DailyAnalyticsData

	// Base query
	query := ds.DB.Table("notes").
		Select("date, COUNT(*) as count").
		Group("date").
		Order("date")

	// Apply date range filter
	switch {
	case startDate != "" && endDate != "":
		query = query.Where("date >= ? AND date <= ?", startDate, endDate)
	case startDate != "":
		query = query.Where("date >= ?", startDate)
	case endDate != "":
		query = query.Where("date <= ?", endDate)
	}

	// Apply species filter
	if species != "" {
		query = query.Where("scientific_name = ? OR common_name = ?", species, species)
	}

	// Execute query
	if err := query.Scan(&analytics).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_daily_analytics_data").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Context("species", species).
			Build()
	}

	return analytics, nil
}

// GetDetectionTrends calculates the trend in detections over time
func (ds *DataStore) GetDetectionTrends(period string, limit int) ([]DailyAnalyticsData, error) {
	var trends []DailyAnalyticsData

	var interval string
	switch period {
	case "week":
		interval = "7 days"
	case "month":
		interval = "30 days"
	case "year":
		interval = "365 days"
	default:
		interval = "30 days" // Default to month
	}

	// Calculate start date based on the period
	var startDate string
	switch strings.ToLower(ds.Dialector().Name()) {
	case "sqlite":
		startDate = fmt.Sprintf("date('now', '-%s')", interval)
		query := fmt.Sprintf(`
			SELECT date, COUNT(*) as count
			FROM notes
			WHERE date >= %s
			GROUP BY date
			ORDER BY date DESC
			LIMIT ?
		`, startDate)

		if err := ds.DB.Raw(query, limit).Scan(&trends).Error; err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_detection_trends_sqlite").
				Context("period", period).
				Context("limit", fmt.Sprintf("%d", limit)).
				Build()
		}
	case "mysql":
		startDate = fmt.Sprintf("DATE_SUB(CURRENT_DATE, INTERVAL %s)", interval)
		query := fmt.Sprintf(`
			SELECT date, COUNT(*) as count
			FROM notes
			WHERE date >= %s
			GROUP BY date
			ORDER BY date DESC
			LIMIT ?
		`, startDate)

		if err := ds.DB.Raw(query, limit).Scan(&trends).Error; err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_detection_trends_mysql").
				Context("period", period).
				Context("limit", fmt.Sprintf("%d", limit)).
				Build()
		}
	default:
		// Safely get database type for error context
		dialectName := "unknown"
		if d := ds.Dialector(); d != nil {
			dialectName = d.Name()
		}
		return nil, errors.Newf("unsupported database dialect").
			Component("datastore").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_detection_trends").
			Context("dialect", dialectName).
			Build()
	}

	return trends, nil
}

// GetHourlyDistribution retrieves hourly detection distribution across a date range
// Groups detections by hour of day (0-23) regardless of the specific date
func (ds *DataStore) GetHourlyDistribution(startDate, endDate, species string) ([]HourlyDistributionData, error) {
	var parsedStartDate, parsedEndDate time.Time
	var err error

	// Only parse start date if provided
	if startDate != "" {
		parsedStartDate, err = time.Parse("2006-01-02", startDate)
		if err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryValidation).
				Context("operation", "get_hourly_distribution").
				Context("start_date", startDate).
				Build()
		}
	}

	// Only parse end date if provided
	if endDate != "" {
		parsedEndDate, err = time.Parse("2006-01-02", endDate)
		if err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryValidation).
				Context("operation", "get_hourly_distribution").
				Context("end_date", endDate).
				Build()
		}
	}

	// Ensure start date is before or equal to end date only if both were provided
	if startDate != "" && endDate != "" {
		if parsedStartDate.After(parsedEndDate) {
			return nil, errors.Newf("start date cannot be after end date").
				Component("datastore").
				Category(errors.CategoryValidation).
				Context("operation", "get_hourly_distribution").
				Context("start_date", startDate).
				Context("end_date", endDate).
				Build()
		}
	}

	// Prepare the SQL query
	query := ds.DB.Table("notes")

	// Extract hour from the time field using database-specific hour format
	hourExpr := ds.GetHourFormat()
	query = query.Select(fmt.Sprintf("%s AS hour, COUNT(*) AS count", hourExpr))

	// Apply date range filter conditionally
	switch {
	case startDate != "" && endDate != "":
		query = query.Where("date BETWEEN ? AND ?", startDate, endDate)
	case startDate != "":
		query = query.Where("date >= ?", startDate)
	case endDate != "":
		query = query.Where("date <= ?", endDate)
		// No date filter if both are empty
	}

	// Apply species filter if provided
	if species != "" {
		// Try to match on either common_name or scientific_name
		query = query.Where("common_name = ? OR scientific_name = ?",
			species, species)
	}

	// Group by hour
	query = query.Group(hourExpr)

	// Order by hour
	query = query.Order("hour ASC")

	// Execute the query
	var results []HourlyDistributionData
	if err := query.Find(&results).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_distribution").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Context("species", species).
			Build()
	}

	return results, nil
}

// GetSpeciesFirstDetectionInPeriod finds the first detection of each species within a specific date range.
// This is suitable for seasonal and yearly tracking where we need to know when each species
// was first detected within that specific period, regardless of prior detections.
// It returns all species detected in the period with their first detection date in that period.
func (ds *DataStore) GetSpeciesFirstDetectionInPeriod(startDate, endDate string, limit, offset int) ([]NewSpeciesData, error) {
	// Validate input
	if startDate != "" && endDate != "" && startDate > endDate {
		return nil, errors.Newf("start date cannot be after end date").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_species_first_detection_in_period").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	// Default pagination values
	if limit <= 0 {
		limit = 10000 // Higher default for period tracking
	}

	type Result struct {
		ScientificName     string
		CommonName         string
		FirstDetectionDate string
		CountInPeriod      int
	}
	var results []Result

	// Query to find the first detection of each species within the specified period
	query := `
	SELECT 
		scientific_name,
		MAX(common_name) as common_name,
		MIN(date) as first_detection_date,
		COUNT(*) as count_in_period
	FROM notes
	WHERE date BETWEEN ? AND ?
		AND date != ''
		AND date IS NOT NULL
	GROUP BY scientific_name
	ORDER BY first_detection_date ASC
	LIMIT ? OFFSET ?
	`

	if err := ds.DB.Raw(query, startDate, endDate, limit, offset).Scan(&results).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_first_detection_in_period").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	// Convert to NewSpeciesData format
	speciesData := make([]NewSpeciesData, len(results))
	for i, r := range results {
		speciesData[i] = NewSpeciesData{
			ScientificName: r.ScientificName,
			CommonName:     r.CommonName,
			FirstSeenDate:  r.FirstDetectionDate,
			CountInPeriod:  r.CountInPeriod,
		}
	}

	return speciesData, nil
}

// GetNewSpeciesDetections finds species whose absolute first detection falls within the specified date range.
// This is suitable for lifetime tracking only - NOT for seasonal or yearly tracking.
// It supports pagination with limit and offset parameters.
// NOTE: For optimal performance with large datasets, add a composite index on (scientific_name, date)
func (ds *DataStore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]NewSpeciesData, error) {
	// Temporary struct to scan raw results, ensuring date can be checked for null/empty
	type RawNewSpeciesResult struct {
		ScientificName     string
		CommonName         string
		FirstDetectionDate string // Scan directly into string
		CountInPeriod      int
	}
	var rawResults []RawNewSpeciesResult

	// Default pagination values if not specified
	if limit <= 0 {
		limit = 100 // Default limit
	}
	// Offset defaults to 0 if negative

	// Ensure start date is before or equal to end date using string comparison (safe for YYYY-MM-DD)
	if startDate != "" && endDate != "" && startDate > endDate {
		return nil, errors.Newf("start date cannot be after end date").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_new_species_detections").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	// Revised query with pagination
	// NOTE: This query benefits significantly from a composite index on (scientific_name, date)
	// Uses ANY_VALUE for MySQL compatibility
	query := `
	WITH SpeciesFirstSeen AS (
	    SELECT 
	        scientific_name, 
	        MIN(CASE WHEN date != '' AND date IS NOT NULL THEN date ELSE NULL END) as first_detection_date
	    FROM notes
	    GROUP BY scientific_name
	    HAVING first_detection_date IS NOT NULL AND first_detection_date != '' 
	), 
	SpeciesInPeriod AS (
	    SELECT 
	        scientific_name, 
	        COUNT(*) as count_in_period,
			MAX(common_name) as common_name -- Reverted from ANY_VALUE for testing
	    FROM notes
	    WHERE date BETWEEN ? AND ?
	    GROUP BY scientific_name
	)
	SELECT 
	    sfs.scientific_name, 
	    COALESCE(sip.common_name, sfs.scientific_name) as common_name, 
	    sfs.first_detection_date, 
	    sip.count_in_period
	FROM SpeciesFirstSeen sfs
	JOIN SpeciesInPeriod sip ON sfs.scientific_name = sip.scientific_name
	WHERE sfs.first_detection_date BETWEEN ? AND ?
	ORDER BY sfs.first_detection_date DESC
	LIMIT ? OFFSET ?;
	`

	// Execute the raw SQL query into the temporary struct
	if err := ds.DB.Raw(query, startDate, endDate, startDate, endDate, limit, offset).Scan(&rawResults).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_new_species_detections").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Context("limit", fmt.Sprintf("%d", limit)).
			Context("offset", fmt.Sprintf("%d", offset)).
			Build()
	}

	// Filter results in Go to ensure FirstDetectionDate is valid before final assignment
	var finalResults []NewSpeciesData
	for _, raw := range rawResults {
		// Explicitly check if the scanned date is non-empty
		if raw.FirstDetectionDate != "" {
			finalResults = append(finalResults, NewSpeciesData{
				ScientificName: raw.ScientificName,
				CommonName:     raw.CommonName,
				FirstSeenDate:  raw.FirstDetectionDate, // Assign only if valid
				CountInPeriod:  raw.CountInPeriod,
			})
		} else {
			// Log if a record surprisingly had an empty date after SQL filtering
			getLogger().Warn("GetNewSpeciesDetections: Skipped record due to empty first_detection_date",
				"scientific_name", raw.ScientificName,
				"operation", "get_new_species_detections")
		}
	}

	return finalResults, nil
}
