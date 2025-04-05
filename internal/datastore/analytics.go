// internal/datastore/analytics.go
package datastore

import (
	"fmt"
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

// GetSpeciesSummaryData retrieves overall statistics for all bird species
func (ds *DataStore) GetSpeciesSummaryData() ([]SpeciesSummaryData, error) {
	var summaries []SpeciesSummaryData

	// SQL query to get species summary data
	// This includes: count, first/last detection, and confidence stats
	query := `
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
		GROUP BY scientific_name
		ORDER BY count DESC
	`

	rows, err := ds.DB.Raw(query).Rows()
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
