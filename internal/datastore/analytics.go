// internal/datastore/analytics.go
package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

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

// ActivityHeatmapData is the columnar, sparse aggregation behind the seasonal density
// heatmap: detection counts bucketed by (station-local calendar date, intra-day slot).
//
// Dates lists every calendar date in the requested range, ascending, and forms the x-axis;
// SlotResolutionMinutes is the effective intra-day slot width (15, 30, or 60), downsampled
// from 15 on wide ranges so the payload stays bounded. The three Cell* slices are parallel
// and hold only non-zero cells: cell i is Dates[CellDateIndex[i]] at slot CellSlot[i] with
// CellCount[i] detections. A slot s covers the wall-clock minutes [s*res, (s+1)*res).
type ActivityHeatmapData struct {
	Dates                 []string
	SlotResolutionMinutes int
	CellDateIndex         []int
	CellSlot              []int
	CellCount             []int
}

// SpeciesHourlyDistribution is one species' normalized hour-of-day activity distribution,
// behind the "who sings when" ridgeline (design spec section 6.2).
//
// Buckets holds 24 values (index = hour 0..23, station-local) that sum to 1.0 for a species
// with any detections in range, so each species' shape is comparable regardless of its raw
// volume. Total is the species' detection count over the range (false positives excluded),
// used to rank species by volume and shown in the tooltip. ScientificName is the stable key;
// the localized common name is resolved client-side (the v2 label schema stores no common name).
type SpeciesHourlyDistribution struct {
	ScientificName string
	Buckets        [24]float64
	Total          int
}

// DailyActivityOnset is one calendar day's dawn-chorus onset relative to civil dawn, behind the
// dawn-chorus onset tracker (design spec section 6.3).
//
// Date is the station-local calendar day (YYYY-MM-DD). OnsetRelMinutes is the onset minute-of-day
// (the time by which the morning chorus has clearly begun) minus civil dawn's minute-of-day, so a
// negative value means the chorus started before civil dawn. It is nil when the day had too few
// detections to be meaningful, or when civil dawn is undefined for the date (polar day / white
// nights / polar night). DetectionCount is the day's false-positive-excluded detection count,
// surfaced in the tooltip. The aggregation emits one entry per calendar day in the requested range
// (DetectionCount is 0 on quiet days) so the chart has a continuous date axis and its trend line
// breaks over gaps rather than interpolating across them.
type DailyActivityOnset struct {
	Date            string
	OnsetRelMinutes *int
	DetectionCount  int
}

// SpeciesConfidenceHistogram is one species' confidence-score distribution, behind the confidence
// distribution chart (design spec section 6.5). Bins holds the normalized fraction of the species'
// detections that fall into each equal-width confidence bin over [0,1] (Bins sums to ~1.0), so the
// distribution shape is comparable across species regardless of detection volume. Total is the
// species' detection count over the range (false positives excluded), shown in the tooltip.
// ScientificName is the stable key; the localized common name is resolved client-side (the v2 label
// schema stores no common name), matching the sibling species charts.
type SpeciesConfidenceHistogram struct {
	ScientificName string
	Bins           []float64
	Total          int
}

// SpeciesAccumulationPoint is one day on the species accumulation curve (the biodiversity collector's
// curve). Date is the station-local calendar day (YYYY-MM-DD). CumulativeSpecies is the running count
// of distinct species first detected on or before this day within the selected range; NewSpecies is
// how many of them first appeared on this day. "First seen" is bounded to the queried window, not
// lifetime, so the curve answers "how fast is the species list filling up over this period".
type SpeciesAccumulationPoint struct {
	Date              string
	CumulativeSpecies int
	NewSpecies        int
}

// AudioSourceSummary describes one audio source for the analytics source/mic filter: a stable opaque
// ID, the source's display metadata, and how many (false-positive-excluded) detections it has in the
// queried range. The API layer turns DisplayName/NodeName into a user-facing label (anonymized for
// unauthenticated clients) and serialises ID as a string. The metric is v2only; the legacy datastore
// does not persist a detection's source, so it returns an empty result.
type AudioSourceSummary struct {
	// ID is the audio source's stable, opaque identifier (the v2 audio_sources primary key).
	ID uint
	// DisplayName is the user-configured source name, empty when unset.
	DisplayName string
	// NodeName is the capture node name, used as a fallback label when DisplayName is empty.
	NodeName string
	// SourceType is the source kind (e.g. "rtsp", "alsa", "file"), used for anonymized labelling.
	SourceType string
	// Count is the number of (false-positive-excluded) detections from this source in the range.
	Count int
}

// YearOverYearPoint is one calendar position on the year-over-year tracker. Date is the current-year
// station-local calendar day (YYYY-MM-DD) used for the x-axis; MonthDay ("MM-DD") is the year-
// independent alignment key shared by both years. ThisYear and LastYear are the cumulative detection
// counts through this calendar position in the current and previous year respectively (false positives
// excluded); Delta is ThisYear - LastYear (positive means the current year is running ahead of last).
type YearOverYearPoint struct {
	Date     string
	MonthDay string
	ThisYear int
	LastYear int
	Delta    int
}

// YearOverYearResult is the year-over-year tracker aggregation: the two calendar years being compared
// and one cumulative point per current-year calendar day from Jan 1 through the requested date. Points
// is always non-nil. It answers "are we ahead of or behind last year's detection activity so far?".
type YearOverYearResult struct {
	CurrentYear  int
	PreviousYear int
	Points       []YearOverYearPoint
}

// SpeciesPhenologyPoint is one species' residency span within the selected date range: its first and
// last false-positive-excluded detection (as station-local YYYY-MM-DD dates) and the in-range
// detection count. Species are the top-N by detection volume; the chart draws one residency bar per
// species (a Gantt) to show arrival/departure timing.
type SpeciesPhenologyPoint struct {
	ScientificName string
	FirstSeen      string
	LastSeen       string
	Count          int
}

// SpeciesHourlyCounts is one species' raw hour-of-day detection counts, behind the acoustic
// succession streamgraph (design spec #1155; Tier-2). Counts holds the species' false-positive-
// excluded detection count in each station-local hour bucket (index = hour 0..23). Unlike the
// ridgeline's SpeciesHourlyDistribution (which normalizes each species to sum to 1.0 to compare
// timing shape), the streamgraph stacks the raw counts so band width is detection volume; Total is
// the sum of Counts, used to rank species by volume and shown in the tooltip. ScientificName is the
// stable key; the localized common name is resolved client-side (the v2 label schema stores no
// common name), matching the sibling species charts.
type SpeciesHourlyCounts struct {
	ScientificName string
	Counts         [24]int
	Total          int
}

// NewSpeciesData represents a species detected for the first time within a period
type NewSpeciesData struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	FirstSeenDate  string `json:"first_seen_date"` // The absolute first date
	LastSeenDate   string `json:"last_seen_date"`  // The most recent detection date
	CountInPeriod  int    `json:"count_in_period"` // Optional: How many times seen in the query period
}

// SpeciesDetectionDate represents a species detected on a specific calendar date.
type SpeciesDetectionDate struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	Date           string `json:"date"`
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

	// Start building query - exclude detections marked as false_positive
	queryStr := fmt.Sprintf(`
		SELECT
			notes.scientific_name,
			COALESCE(MAX(notes.common_name), '') as common_name,
			COALESCE(MAX(notes.species_code), '') as species_code,
			COUNT(*) as count,
			MIN(%s) as first_seen,
			MAX(%s) as last_seen,
			AVG(notes.confidence) as avg_confidence,
			MAX(notes.confidence) as max_confidence
		FROM notes
		LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
	`, dateTimeFormat, dateTimeFormat)

	// Add WHERE clause for false_positive filtering and date filters
	var whereClause string
	var args []any

	// Always exclude false positives
	whereClause = fmt.Sprintf("WHERE (note_reviews.verified IS NULL OR note_reviews.verified != '%s')",
		entities.VerificationFalsePositive)

	// Add date filters if provided
	switch {
	case startDate != "" && endDate != "":
		whereClause += " AND notes.date >= ? AND notes.date <= ?"
		args = append(args, startDate, endDate)
	case startDate != "":
		whereClause += " AND notes.date >= ?"
		args = append(args, startDate)
	case endDate != "":
		whereClause += " AND notes.date <= ?"
		args = append(args, endDate)
	}

	// Complete the query
	queryStr += whereClause + `
		GROUP BY notes.scientific_name
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

	// Track transaction duration for performance monitoring
	txStart := time.Now()

	// Use GORM's Transaction helper for automatic commit/rollback handling.
	// The query is bounded by the caller's request context, which the API layer
	// wraps with analyticsQueryTimeout (internal/api/v2/analytics.go). The v2only
	// implementation relies on the caller's context the same way.
	err := ds.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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

		// The query is bounded by the caller's request context (the API layer wraps
		// it with analyticsQueryTimeout). Check cancellation inside the scan loop so a
		// client disconnect or deadline on a large result set surfaces immediately
		// instead of after scanning every remaining row.
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}
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

	// Base query - exclude detections marked as false_positive
	query := ds.DB.WithContext(ctx).Table("notes").
		Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
		Select(fmt.Sprintf("%s as hour, COUNT(*) as count", hourFormat)).
		Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", string(entities.VerificationFalsePositive)).
		Group(hourFormat).
		Order("hour")

	// Apply filters
	if date != "" {
		query = query.Where("notes.date = ?", date)
	}

	if species != "" {
		query = query.Where("notes.scientific_name = ? OR notes.common_name = ?", species, species)
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

	// Base query - exclude detections marked as false_positive
	query := ds.DB.WithContext(ctx).Table("notes").
		Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
		Select("notes.date, COUNT(*) as count").
		Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", string(entities.VerificationFalsePositive)).
		Group("notes.date").
		Order("notes.date")

	// Apply date range filter
	switch {
	case startDate != "" && endDate != "":
		query = query.Where("notes.date >= ? AND notes.date <= ?", startDate, endDate)
	case startDate != "":
		query = query.Where("notes.date >= ?", startDate)
	case endDate != "":
		query = query.Where("notes.date <= ?", endDate)
	}

	// Apply species filter
	if species != "" {
		query = query.Where("notes.scientific_name = ? OR notes.common_name = ?", species, species)
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

// GetSpeciesDiversityData retrieves the count of unique species detected per day
func (ds *DataStore) GetSpeciesDiversityData(ctx context.Context, startDate, endDate string) ([]DailyAnalyticsData, error) {
	var diversity []DailyAnalyticsData

	// Base query - count distinct species per day, excluding false positives
	query := ds.DB.WithContext(ctx).Table("notes").
		Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
		Select("notes.date, COUNT(DISTINCT notes.scientific_name) as count").
		Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", string(entities.VerificationFalsePositive)).
		Group("notes.date").
		Order("notes.date")

	// Apply date range filter
	switch {
	case startDate != "" && endDate != "":
		query = query.Where("notes.date >= ? AND notes.date <= ?", startDate, endDate)
	case startDate != "":
		query = query.Where("notes.date >= ?", startDate)
	case endDate != "":
		query = query.Where("notes.date <= ?", endDate)
	}

	// Execute query
	if err := query.Scan(&diversity).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_diversity_data").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	return diversity, nil
}

// GetActivityHeatmap is a stub on the legacy store. The seasonal density heatmap is a v2only
// feature; the legacy datastore is deprecated and being removed, so it returns an empty grid
// rather than implementing the aggregation. See internal/datastore/v2only for the real method.
func (ds *DataStore) GetActivityHeatmap(_ context.Context, _, _, _ string) (ActivityHeatmapData, error) {
	// Return a structurally valid empty grid (non-nil slices, a real slot resolution) rather
	// than a zero value, so consumers never see SlotResolutionMinutes == 0.
	return ActivityHeatmapData{
		Dates:                 []string{},
		SlotResolutionMinutes: 15,
		CellDateIndex:         []int{},
		CellSlot:              []int{},
		CellCount:             []int{},
	}, nil
}

// GetHourlyDistributionBySpecies is a stub on the legacy store. The who-sings-when ridgeline is a
// v2only feature; the legacy datastore is deprecated and being removed, so it returns an empty
// (non-nil) slice rather than implementing the aggregation. See internal/datastore/v2only for the
// real method.
func (ds *DataStore) GetHourlyDistributionBySpecies(_ context.Context, _, _ string, _ int) ([]SpeciesHourlyDistribution, error) {
	return []SpeciesHourlyDistribution{}, nil
}

// GetDailyActivityOnset is a stub on the legacy store. The dawn-chorus onset tracker is a v2only
// feature; the legacy datastore is deprecated and being removed, so it returns an empty (non-nil)
// slice rather than implementing the aggregation. See internal/datastore/v2only for the real method.
func (ds *DataStore) GetDailyActivityOnset(_ context.Context, _, _, _ string) ([]DailyActivityOnset, error) {
	return []DailyActivityOnset{}, nil
}

// GetConfidenceHistogram is a stub on the legacy store. The confidence distribution chart is a
// v2only feature; the legacy datastore is deprecated and being removed, so it returns an empty
// (non-nil) slice rather than implementing the aggregation. See internal/datastore/v2only for the
// real method.
func (ds *DataStore) GetConfidenceHistogram(_ context.Context, _, _, _ string, _, _ int) ([]SpeciesConfidenceHistogram, error) {
	return []SpeciesConfidenceHistogram{}, nil
}

// GetSpeciesAccumulation is a stub on the legacy store. The species accumulation curve is a v2only
// feature; the legacy datastore is deprecated and being removed, so it returns an empty (non-nil)
// slice rather than implementing the aggregation. See internal/datastore/v2only for the real method.
func (ds *DataStore) GetSpeciesAccumulation(_ context.Context, _, _ string) ([]SpeciesAccumulationPoint, error) {
	return []SpeciesAccumulationPoint{}, nil
}

// GetAudioSources is a stub on the legacy store. The analytics source/mic filter is a v2only feature:
// the legacy schema does not persist a detection's audio source, so there is nothing to group by. It
// returns an empty (non-nil) slice rather than implementing the aggregation. See internal/datastore/v2only
// for the real method.
func (ds *DataStore) GetAudioSources(_ context.Context, _, _ string) ([]AudioSourceSummary, error) {
	return []AudioSourceSummary{}, nil
}

// GetYearOverYear is a stub on the legacy store. The year-over-year tracker is a v2only feature; the
// legacy datastore is deprecated and being removed, so it returns an empty (non-nil) result rather
// than implementing the aggregation. See internal/datastore/v2only for the real method.
func (ds *DataStore) GetYearOverYear(_ context.Context, _ string) (YearOverYearResult, error) {
	return YearOverYearResult{Points: []YearOverYearPoint{}}, nil
}

// GetSpeciesPhenology is a stub on the legacy store. The arrival/departure phenology chart is a
// v2only feature; the legacy datastore is deprecated and being removed, so it returns an empty
// (non-nil) slice rather than implementing the aggregation. See internal/datastore/v2only for the
// real method.
func (ds *DataStore) GetSpeciesPhenology(_ context.Context, _, _ string, _ int) ([]SpeciesPhenologyPoint, error) {
	return []SpeciesPhenologyPoint{}, nil
}

// GetAcousticSuccession is a stub on the legacy store. The acoustic succession streamgraph is a
// v2only feature; the legacy datastore is deprecated and being removed, so it returns an empty
// (non-nil) slice rather than implementing the aggregation. See internal/datastore/v2only for the
// real method.
func (ds *DataStore) GetAcousticSuccession(_ context.Context, _, _ string, _ int) ([]SpeciesHourlyCounts, error) {
	return []SpeciesHourlyCounts{}, nil
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
		parsedStartDate, err = time.Parse(time.DateOnly, startDate)
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
		parsedEndDate, err = time.Parse(time.DateOnly, endDate)
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

	// Prepare the SQL query - exclude detections marked as false_positive
	query := ds.DB.WithContext(ctx).Table("notes").
		Joins("LEFT JOIN note_reviews ON notes.id = note_reviews.note_id").
		Where("(note_reviews.verified IS NULL OR note_reviews.verified != ?)", string(entities.VerificationFalsePositive))

	// Extract hour from the time field using database-specific hour format
	hourExpr := ds.GetHourFormat()
	query = query.Select(fmt.Sprintf("%s AS hour, COUNT(*) AS count", hourExpr))

	// Apply date range filter conditionally
	switch {
	case startDate != "" && endDate != "":
		query = query.Where("notes.date BETWEEN ? AND ?", startDate, endDate)
	case startDate != "":
		query = query.Where("notes.date >= ?", startDate)
	case endDate != "":
		query = query.Where("notes.date <= ?", endDate)
		// No date filter if both are empty
	}

	// Apply species filter if provided
	if species != "" {
		// Try to match on either common_name or scientific_name
		query = query.Where("notes.common_name = ? OR notes.scientific_name = ?",
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

// GetSpeciesDetectionDatesInPeriod returns distinct species/date pairs within a period.
func (ds *DataStore) GetSpeciesDetectionDatesInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]SpeciesDetectionDate, error) {
	if startDate != "" && endDate != "" && startDate > endDate {
		return nil, errors.Newf("start date cannot be after end date").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "get_species_detection_dates_in_period").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	if limit <= 0 {
		limit = 10000
	}

	query := fmt.Sprintf(`
	SELECT
		notes.scientific_name,
		MAX(notes.common_name) as common_name,
		notes.date
	FROM notes
	LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
	WHERE notes.date BETWEEN ? AND ?
		AND notes.date != ''
		AND notes.date IS NOT NULL
		AND (note_reviews.verified IS NULL OR note_reviews.verified != '%s')
	GROUP BY notes.scientific_name, notes.date
	ORDER BY notes.date ASC, notes.scientific_name ASC
	LIMIT ? OFFSET ?
	`, entities.VerificationFalsePositive)

	var results []SpeciesDetectionDate
	if err := ds.DB.WithContext(ctx).Raw(query, startDate, endDate, limit, offset).Scan(&results).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_detection_dates_in_period").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	return results, nil
}

// GetSpeciesLastDetectionDateBefore returns the last detection date before the given date.
func (ds *DataStore) GetSpeciesLastDetectionDateBefore(ctx context.Context, scientificName, beforeDate string) (string, error) {
	query := fmt.Sprintf(`
	SELECT COALESCE(MAX(notes.date), '') as last_seen_date
	FROM notes
	LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
	WHERE notes.scientific_name = ?
		AND notes.date < ?
		AND notes.date != ''
		AND notes.date IS NOT NULL
		AND (note_reviews.verified IS NULL OR note_reviews.verified != '%s')
	`, entities.VerificationFalsePositive)

	var result struct {
		LastSeenDate string `gorm:"column:last_seen_date"`
	}
	if err := ds.DB.WithContext(ctx).Raw(query, scientificName, beforeDate).Scan(&result).Error; err != nil {
		return "", errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_last_detection_date_before").
			Context("scientific_name", scientificName).
			Context("before_date", beforeDate).
			Build()
	}

	return result.LastSeenDate, nil
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
		LastDetectionDate  string
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
	// Excludes detections marked as false_positive from both CTEs
	query := fmt.Sprintf(`
	WITH SpeciesFirstSeen AS (
	    SELECT
	        notes.scientific_name,
	        MIN(CASE WHEN notes.date != '' AND notes.date IS NOT NULL THEN notes.date ELSE NULL END) as first_detection_date,
	        MAX(CASE WHEN notes.date != '' AND notes.date IS NOT NULL THEN notes.date ELSE NULL END) as last_detection_date
	    FROM notes
	    LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
	    WHERE (note_reviews.verified IS NULL OR note_reviews.verified != '%s')
	    GROUP BY notes.scientific_name
	    HAVING first_detection_date IS NOT NULL AND first_detection_date != ''
	),
	SpeciesInPeriod AS (
	    SELECT
	        notes.scientific_name,
	        COUNT(*) as count_in_period,
			MAX(notes.common_name) as common_name
	    FROM notes
	    LEFT JOIN note_reviews ON notes.id = note_reviews.note_id
	    WHERE notes.date BETWEEN ? AND ?
	      AND (note_reviews.verified IS NULL OR note_reviews.verified != '%s')
	    GROUP BY notes.scientific_name
	)
	SELECT
	    sfs.scientific_name,
	    COALESCE(sip.common_name, sfs.scientific_name) as common_name,
	    sfs.first_detection_date,
	    sfs.last_detection_date,
	    sip.count_in_period
	FROM SpeciesFirstSeen sfs
	JOIN SpeciesInPeriod sip ON sfs.scientific_name = sip.scientific_name
	WHERE sfs.first_detection_date BETWEEN ? AND ?
	ORDER BY sfs.first_detection_date DESC
	LIMIT ? OFFSET ?;
	`, entities.VerificationFalsePositive, entities.VerificationFalsePositive)

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
				LastSeenDate:   raw.LastDetectionDate,
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
