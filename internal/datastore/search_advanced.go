// Package datastore provides advanced search functionality
package datastore

import (
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

// AdvancedSearchFilters represents all possible search filters from the frontend
type AdvancedSearchFilters struct {
	TextQuery     string
	Confidence    *ConfidenceFilter
	TimeOfDay     []string // ["dawn", "day", "dusk", "night"]
	Hour          *HourFilter
	DateRange     *DateRange
	Verified      *bool
	Species       []string
	Location      []string // Maps to source field (legacy file source)
	Source        []string // Maps to source_label field (RTSP stream labels)
	Locked        *bool
	SortAscending bool
	Limit         int
	Offset        int
}

// ConfidenceFilter represents a confidence level filter
type ConfidenceFilter struct {
	Operator string // ">", "<", ">=", "<=", "="
	Value    float64
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

	query := ds.DB.Model(&Note{}).
		Joins("LEFT JOIN audio_source_records ON notes.audio_source_id = audio_source_records.id").
		Preload("Review").
		Preload("Lock").
		Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at DESC")
		})

	// Apply text search if provided
	if filters.TextQuery != "" {
		query = query.Where("common_name LIKE ? OR scientific_name LIKE ?",
			"%"+filters.TextQuery+"%", "%"+filters.TextQuery+"%")
	}

	// Apply confidence filter
	query = applyConfidenceFilter(query, filters.Confidence)

	// Apply date range filter
	query = applyDateRangeFilter(query, filters.DateRange)

	// Apply hour filter
	query = applyHourFilter(query, filters.Hour)

	// Apply time of day filter
	query = applyTimeOfDayFilter(query, filters.TimeOfDay)

	// Apply species filter
	if len(filters.Species) > 0 {
		query = query.Where("species_code IN ? OR scientific_name IN ?", filters.Species, filters.Species)
	}

	// Apply location/source filter (legacy file source)
	if len(filters.Location) > 0 {
		query = query.Where("source IN ?", filters.Location)
	}

	if len(filters.Source) > 0 {
		query = query.Where("audio_source_records.label IN ?", filters.Source)
	}

	// Apply verified filter
	query = applyVerifiedFilter(query, filters.Verified)

	// Apply locked filter
	query = applyLockedFilter(query, filters.Locked)

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

	// Apply sorting
	order := "DESC"
	if filters.SortAscending {
		order = "ASC"
	}
	query = query.Order("id " + order)

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
		return time.Parse("2006-01-02", shortcut)
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

// applyDateRangeFilter applies date range filtering to the query
func applyDateRangeFilter(query *gorm.DB, dateRange *DateRange) *gorm.DB {
	if dateRange == nil {
		return query
	}

	startDate := dateRange.Start.Format("2006-01-02")
	endDate := dateRange.End.Format("2006-01-02")
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

// applyTimeOfDayFilter applies time of day filtering to the query
func applyTimeOfDayFilter(query *gorm.DB, timeOfDay []string) *gorm.DB {
	if len(timeOfDay) == 0 {
		return query
	}

	var timeConditions []string
	var args []any

	for _, tod := range timeOfDay {
		switch strings.ToLower(tod) {
		case "dawn":
			// Approximate dawn as 5-7 AM
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "05:00:00", "07:00:00")
		case "day":
			// Approximate day as 7 AM - 6 PM
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "07:00:00", "18:00:00")
		case "dusk":
			// Approximate dusk as 6-8 PM
			timeConditions = append(timeConditions, "(time >= ? AND time < ?)")
			args = append(args, "18:00:00", "20:00:00")
		case "night":
			// Approximate night as 8 PM - 5 AM
			timeConditions = append(timeConditions, "(time >= ? OR time < ?)")
			args = append(args, "20:00:00", "05:00:00")
		}
	}

	if len(timeConditions) > 0 {
		return query.Where("("+strings.Join(timeConditions, " OR ")+")", args...)
	}

	return query
}

// applyVerifiedFilter applies verified filtering to the query
func applyVerifiedFilter(query *gorm.DB, verified *bool) *gorm.DB {
	if verified == nil {
		return query
	}

	if *verified {
		return query.Joins("INNER JOIN note_reviews ON note_reviews.note_id = notes.id AND note_reviews.verified != ''")
	}

	return query.Joins("LEFT JOIN note_reviews ON note_reviews.note_id = notes.id").
		Where("note_reviews.id IS NULL OR note_reviews.verified = ''")
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
