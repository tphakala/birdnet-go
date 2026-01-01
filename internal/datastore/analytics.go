// internal/datastore/analytics.go
package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// defaultSourceLabel is the display label for detections from the local microphone (no RTSP label)
const defaultSourceLabel = "Local Microphone"

// isDebugLoggingEnabled returns true if debug logging is enabled and logger is available
func isDebugLoggingEnabled() bool {
	settings := conf.GetSettings()
	return settings != nil && settings.Debug
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
//
// NOTE: Uses a read-only transaction with repeatable read isolation to prevent race conditions
// when concurrent writes are occurring. This ensures consistent timestamps even when new species
// are being inserted. See issue #1239 for details on the SQLite WAL mode race condition.
func (ds *DataStore) GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]SpeciesSummaryData, error) {
	// Pre-allocate with reasonable capacity for typical species count
	summaries := make([]SpeciesSummaryData, 0, 100)

	// Track query time for performance monitoring
	queryStart := time.Now()

	// Debug logging for query start
	if isDebugLoggingEnabled() {
		GetLogger().Debug("GetSpeciesSummaryData: Starting query",
			logger.String("start_date", startDate),
			logger.String("end_date", endDate))
	}

	// Get database-specific datetime formatting
	// TODO: Consider using GetDateTimeExpr("notes.date", "notes.time") for future JOIN support
	dateTimeFormat := ds.GetDateTimeFormat()
	if dateTimeFormat == "" {
		// Safely get database type for error context
		dialectName := DialectUnknown
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
			COALESCE(MAX(common_name), '') as common_name,
			COALESCE(MAX(species_code), '') as species_code,
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

	// Execute the query within a read-only transaction for consistent snapshot isolation
	// This prevents race conditions where partial writes are visible during concurrent inserts
	// For SQLite with WAL mode: provides a consistent snapshot view even during concurrent writes
	// For MySQL: uses default REPEATABLE READ isolation level for snapshot consistency
	if isDebugLoggingEnabled() {
		GetLogger().Debug("GetSpeciesSummaryData: Executing query with snapshot isolation",
			logger.String("query", queryStr),
			logger.Any("args", args))
	}

	// Add timeout to prevent indefinite execution
	// Use 30 seconds as a reasonable upper bound for analytics queries
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Track transaction duration for performance monitoring
	txStart := time.Now()

	// Use GORM's Transaction helper for automatic commit/rollback handling
	err := ds.DB.WithContext(ctxWithTimeout).Transaction(func(tx *gorm.DB) error {
		// Execute query within transaction
		rows, err := tx.Raw(queryStr, args...).Rows()
		if err != nil {
			return dbError(err, "get_species_summary_data", errors.PriorityMedium,
				"start_date", startDate,
				"end_date", endDate,
				"action", "generate_species_analytics_report")
		}
		defer func() {
			if err := rows.Close(); err != nil {
				GetLogger().Error("Failed to close rows",
					logger.Error(err),
					logger.String("operation", "get_species_summary_data"))
			}
		}()

		queryExecutionTime := time.Since(queryStart)
		if isDebugLoggingEnabled() {
			GetLogger().Debug("GetSpeciesSummaryData: Query executed, scanning rows",
				logger.Int64("query_duration_ms", queryExecutionTime.Milliseconds()))
		}
		rowCount := 0

		// NOTE: Transaction has 30-second timeout at the top level (ctxWithTimeout)
		// TODO(context-deadline): For very large result sets, consider adding context checks in loop
		// Example: select { case <-ctxWithTimeout.Done(): rows.Close(); return ctxWithTimeout.Err(); default: }
		// TODO(telemetry): Report context cancellations during row processing to internal/telemetry
		for rows.Next() {
			rowCount++
			var summary SpeciesSummaryData
			var firstSeenStr, lastSeenStr string

			if err := rows.Scan(
				&summary.ScientificName,
				&summary.CommonName,
				&summary.SpeciesCode,
				&summary.Count,
				&firstSeenStr,
				&lastSeenStr,
				&summary.AvgConfidence,
				&summary.MaxConfidence,
			); err != nil {
				return dbError(err, "scan_species_summary_data", errors.PriorityLow,
					"action", "parse_analytics_query_results")
			}

			// Parse time strings to time.Time
			// IMPORTANT: Database stores local time strings, parse as local time
			if firstSeenStr != "" {
				firstSeen, err := time.ParseInLocation("2006-01-02 15:04:05", firstSeenStr, time.Local)
				if err == nil {
					summary.FirstSeen = firstSeen
				} else if isDebugLoggingEnabled() {
					GetLogger().Debug("Failed to parse firstSeen time",
						logger.String("species", summary.ScientificName),
						logger.String("firstSeenStr", firstSeenStr),
						logger.Error(err))
				}
			}

			if lastSeenStr != "" {
				lastSeen, err := time.ParseInLocation("2006-01-02 15:04:05", lastSeenStr, time.Local)
				if err == nil {
					summary.LastSeen = lastSeen
				} else if isDebugLoggingEnabled() {
					GetLogger().Debug("Failed to parse lastSeen time",
						logger.String("species", summary.ScientificName),
						logger.String("lastSeenStr", lastSeenStr),
						logger.Error(err))
				}
			}

			summaries = append(summaries, summary)
		}

		// Check for errors from row iteration
		if err := rows.Err(); err != nil {
			return dbError(err, "iterate_species_summary_rows", errors.PriorityLow,
				"action", "process_analytics_results")
		}

		// Log transaction metrics
		txDuration := time.Since(txStart)
		if isDebugLoggingEnabled() {
			GetLogger().Debug("GetSpeciesSummaryData: Transaction completed",
				logger.Int64("tx_duration_ms", txDuration.Milliseconds()),
				logger.Int("rows_processed", rowCount))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	totalDuration := time.Since(queryStart)
	if isDebugLoggingEnabled() {
		GetLogger().Debug("GetSpeciesSummaryData: Completed",
			logger.Int64("total_duration_ms", totalDuration.Milliseconds()),
			logger.Int("rows_processed", len(summaries)))
	}

	return summaries, nil
}

// GetHourlyAnalyticsData retrieves detection counts grouped by hour
func (ds *DataStore) GetHourlyAnalyticsData(ctx context.Context, date, species string) ([]HourlyAnalyticsData, error) {
	var analytics []HourlyAnalyticsData
	hourFormat := ds.GetHourFormat()

	// Base query
	query := ds.DB.WithContext(ctx).Table("notes").
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
func (ds *DataStore) GetDailyAnalyticsData(ctx context.Context, startDate, endDate, species string) ([]DailyAnalyticsData, error) {
	var analytics []DailyAnalyticsData

	// Base query
	query := ds.DB.WithContext(ctx).Table("notes").
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
func (ds *DataStore) GetDetectionTrends(ctx context.Context, period string, limit int) ([]DailyAnalyticsData, error) {
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
	case DialectSQLite:
		startDate = fmt.Sprintf("date('now', '-%s')", interval)
		query := fmt.Sprintf(`
			SELECT date, COUNT(*) as count
			FROM notes
			WHERE date >= %s
			GROUP BY date
			ORDER BY date DESC
			LIMIT ?
		`, startDate)

		if err := ds.DB.WithContext(ctx).Raw(query, limit).Scan(&trends).Error; err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_detection_trends_sqlite").
				Context("period", period).
				Context("limit", fmt.Sprintf("%d", limit)).
				Build()
		}
	case DialectMySQL:
		startDate = fmt.Sprintf("DATE_SUB(CURRENT_DATE, INTERVAL %s)", interval)
		query := fmt.Sprintf(`
			SELECT date, COUNT(*) as count
			FROM notes
			WHERE date >= %s
			GROUP BY date
			ORDER BY date DESC
			LIMIT ?
		`, startDate)

		if err := ds.DB.WithContext(ctx).Raw(query, limit).Scan(&trends).Error; err != nil {
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
		dialectName := DialectUnknown
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
func (ds *DataStore) GetHourlyDistribution(ctx context.Context, startDate, endDate, species string) ([]HourlyDistributionData, error) {
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
	query := ds.DB.WithContext(ctx).Table("notes")

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
func (ds *DataStore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]NewSpeciesData, error) {
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

	if err := ds.DB.WithContext(ctx).Raw(query, startDate, endDate, limit, offset).Scan(&results).Error; err != nil {
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
func (ds *DataStore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]NewSpeciesData, error) {
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
	if err := ds.DB.WithContext(ctx).Raw(query, startDate, endDate, startDate, endDate, limit, offset).Scan(&rawResults).Error; err != nil {
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
			GetLogger().Warn("GetNewSpeciesDetections: Skipped record due to empty first_detection_date",
				logger.String("scientific_name", raw.ScientificName),
				logger.String("operation", "get_new_species_detections"))
		}
	}

	return finalResults, nil
}

// SourceSummaryData contains detection statistics by audio source
type SourceSummaryData struct {
	SourceLabel string `json:"source_label"`
	Count       int    `json:"count"`
}

// GetSourceSummaryData retrieves detection counts grouped by audio source label
func (ds *DataStore) GetSourceSummaryData(ctx context.Context, startDate, endDate string, limit int) ([]SourceSummaryData, error) {
	summaries := make([]SourceSummaryData, 0, 10)

	caseExpr := fmt.Sprintf(
		"CASE WHEN audio_source_records.label IS NULL OR audio_source_records.label = '' THEN '%s' ELSE audio_source_records.label END",
		defaultSourceLabel,
	)

	query := ds.DB.WithContext(ctx).Table("notes").
		Joins("LEFT JOIN audio_source_records ON notes.audio_source_id = audio_source_records.id").
		Select(fmt.Sprintf("%s as source_label, COUNT(*) as count", caseExpr)).
		Group(caseExpr).
		Order("count DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	switch {
	case startDate != "" && endDate != "":
		query = query.Where("notes.date >= ? AND notes.date <= ?", startDate, endDate)
	case startDate != "":
		query = query.Where("notes.date >= ?", startDate)
	case endDate != "":
		query = query.Where("notes.date <= ?", endDate)
	}

	if err := query.Scan(&summaries).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_source_summary_data").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	return summaries, nil
}
