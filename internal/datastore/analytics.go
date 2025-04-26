// internal/datastore/analytics.go
package datastore

import (
	"fmt"
	"log"
	"time"
)

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
	var summaries []SpeciesSummaryData

	// Start building query
	queryStr := `
		SELECT 
			scientific_name,
			MAX(common_name) as common_name,
			species_code,
			COUNT(*) as count,
			MIN(date || ' ' || time) as first_seen,
			MAX(date || ' ' || time) as last_seen,
			AVG(confidence) as avg_confidence,
			MAX(confidence) as max_confidence
		FROM notes
	`

	// Add WHERE clause if date filters are provided
	var whereClause string
	var args []interface{}

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
	rows, err := ds.DB.Raw(queryStr, args...).Rows()
	if err != nil {
		return nil, fmt.Errorf("error getting species summary data: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
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
			return nil, fmt.Errorf("error scanning species summary data: %w", err)
		}

		// Parse time strings to time.Time
		if firstSeenStr != "" {
			firstSeen, err := time.Parse("2006-01-02 15:04:05", firstSeenStr)
			if err == nil {
				summary.FirstSeen = firstSeen
			}
		}

		if lastSeenStr != "" {
			lastSeen, err := time.Parse("2006-01-02 15:04:05", lastSeenStr)
			if err == nil {
				summary.LastSeen = lastSeen
			}
		}

		summaries = append(summaries, summary)
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
		return nil, fmt.Errorf("error getting hourly analytics data: %w", err)
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
		return nil, fmt.Errorf("error getting daily analytics data: %w", err)
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
	switch ds.DB.Dialector.Name() {
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
			return nil, fmt.Errorf("error getting detection trends for SQLite: %w", err)
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
			return nil, fmt.Errorf("error getting detection trends for MySQL: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported database dialect for trends calculation: %s", ds.DB.Dialector.Name())
	}

	return trends, nil
}

// GetHourlyDistribution retrieves hourly detection distribution across a date range
// Groups detections by hour of day (0-23) regardless of the specific date
func (ds *DataStore) GetHourlyDistribution(startDate, endDate, species string) ([]HourlyDistributionData, error) {
	// Parse dates to ensure they're valid
	parsedStartDate, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return nil, fmt.Errorf("invalid start date format: %w", err)
	}

	parsedEndDate, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return nil, fmt.Errorf("invalid end date format: %w", err)
	}

	// Ensure start date is before or equal to end date
	if parsedStartDate.After(parsedEndDate) {
		return nil, fmt.Errorf("start date cannot be after end date")
	}

	// Prepare the SQL query
	query := ds.DB.Table("notes")

	// Extract hour from the time field using database-specific hour format
	// This uses the same helper method used elsewhere for consistent hour extraction
	hourExpr := ds.GetHourFormat()
	query = query.Select(fmt.Sprintf("%s AS hour, COUNT(*) AS count", hourExpr))

	// Apply date range filter
	query = query.Where("date BETWEEN ? AND ?", startDate, endDate)

	// Apply species filter if provided
	if species != "" {
		// Try to match on either common_name or scientific_name
		query = query.Where("common_name LIKE ? OR scientific_name LIKE ?",
			"%"+species+"%", "%"+species+"%")
	}

	// Group by hour
	query = query.Group("hour")

	// Order by hour
	query = query.Order("hour ASC")

	// Execute the query
	var results []HourlyDistributionData
	if err := query.Find(&results).Error; err != nil {
		return nil, fmt.Errorf("failed to retrieve hourly distribution: %w", err)
	}

	return results, nil
}

// GetNewSpeciesDetections finds species whose absolute first detection falls within the specified date range.
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

	// Revised query with pagination
	// NOTE: This query benefits significantly from a composite index on (scientific_name, date)
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
			MAX(common_name) as common_name
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
		return nil, fmt.Errorf("failed to get new species detections raw results: %w", err)
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
			log.Printf("WARN: GetNewSpeciesDetections - Skipped record for %s due to empty first_detection_date after SQL query.", raw.ScientificName)
		}
	}

	return finalResults, nil
}
