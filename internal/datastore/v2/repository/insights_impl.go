package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type insightsRepository struct {
	db          *gorm.DB
	useV2Prefix bool
	isMySQL     bool
}

// NewInsightsRepository creates a new insights repository.
func NewInsightsRepository(db *gorm.DB, useV2Prefix, isMySQL bool) InsightsRepository {
	return &insightsRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

// Table name helpers — same pattern as detectionRepository.
func (r *insightsRepository) detectionsTable() string {
	if r.useV2Prefix {
		return tableV2Detections
	}
	return tableDetections
}

func (r *insightsRepository) labelsTable() string {
	if r.useV2Prefix {
		return tableV2Labels
	}
	return tableLabels
}

func (r *insightsRepository) reviewsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionReviews
	}
	return tableDetectionReviews
}

// SQL expression helpers — same offset-arithmetic pattern as detectionRepository.hourFromUnixExpr.
// offsetSeconds is the configured timezone's UTC offset; it is added to the epoch before the
// date/hour/year is extracted, so the result buckets by wall-clock value in that zone and is
// independent of the database session / OS-local timezone (unlike the older 'localtime' /
// FROM_UNIXTIME forms). detected_at is always positive and offsets are bounded, so the
// arithmetic stays non-negative even for west-of-UTC (negative) offsets.

// dateFromUnixExpr returns a SQL expression for the wall-clock calendar date (YYYY-MM-DD).
// SQLite: date(column + offset, 'unixepoch')
// MySQL:  DATE_ADD('1970-01-01', INTERVAL (column + offset) DIV 86400 DAY)
//
// The MySQL form computes the civil date arithmetically (DATE_ADD on a literal date with an
// integer day count) so it does not depend on the session time_zone; DATE(FROM_UNIXTIME(...))
// would apply the session zone on top of the offset and double-count. Integer DIV avoids any
// floating-point rounding at exact day boundaries.
func (r *insightsRepository) dateFromUnixExpr(column string, offsetSeconds int) string {
	if r.isMySQL {
		return fmt.Sprintf("DATE_ADD('1970-01-01', INTERVAL (%s + %d) DIV 86400 DAY)", column, offsetSeconds)
	}
	return fmt.Sprintf("date(%s + %d, 'unixepoch')", column, offsetSeconds)
}

func (r *insightsRepository) hourFromUnixExpr(column string, offsetSeconds int) string {
	if r.isMySQL {
		return fmt.Sprintf("FLOOR((%s + %d) / 3600) %% 24", column, offsetSeconds)
	}
	return fmt.Sprintf("CAST((%s + %d) / 3600 AS INTEGER) %% 24", column, offsetSeconds)
}

func (r *insightsRepository) yearFromUnixExpr(column string, offsetSeconds int) string {
	if r.isMySQL {
		return fmt.Sprintf("YEAR(DATE_ADD('1970-01-01', INTERVAL (%s + %d) DIV 86400 DAY))", column, offsetSeconds)
	}
	return fmt.Sprintf("CAST(strftime('%%Y', %s + %d, 'unixepoch') AS INTEGER)", column, offsetSeconds)
}

// falsePositiveExclusion returns the standard LEFT JOIN + WHERE clause
// for excluding false positive detections.
// detAlias is the alias for the detections table (e.g., "d").
func (r *insightsRepository) falsePositiveExclusion(detAlias string) (joinClause, whereClause string) {
	revTable := r.reviewsTable()
	joinClause = fmt.Sprintf("LEFT JOIN %s dr ON %s.id = dr.detection_id", revTable, detAlias)
	whereClause = "(dr.verified IS NULL OR dr.verified != 'false_positive')"
	return joinClause, whereClause
}

func (r *insightsRepository) GetExpectedSpeciesToday(ctx context.Context, yearRanges []TimeRange, tzOffsetSeconds int, modelID *uint) ([]ExpectedSpecies, error) {
	if len(yearRanges) == 0 {
		return nil, nil
	}

	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	dateExpr := r.dateFromUnixExpr("d.detected_at", tzOffsetSeconds)
	yearExpr := r.yearFromUnixExpr("d.detected_at", tzOffsetSeconds)

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, COUNT(DISTINCT %s) as years_seen, MAX(%s) as last_seen_date",
			lab, yearExpr, dateExpr)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where(fpWhere)

	// Add year range conditions as OR clauses
	orScope := r.db.Where("1 = 0")
	for _, tr := range yearRanges {
		orScope = orScope.Or("d.detected_at BETWEEN ? AND ?", tr.Start, tr.End)
	}
	query = query.Where(orScope)

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	query = query.Group(lab + ".scientific_name").Order("years_seen DESC")

	var results []ExpectedSpecies
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("expected species query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetPhantomSpecies(ctx context.Context, since int64, minDetections int, maxAvgConfidence float64, modelID *uint) ([]PhantomSpecies, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, COUNT(*) as detection_count, AVG(d.confidence) as avg_confidence, MAX(d.confidence) as max_confidence", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where("d.detected_at >= ?", since).
		Where(fpWhere)

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	query = query.Group(lab+".scientific_name").
		Having("COUNT(*) >= ? AND AVG(d.confidence) < ?", minDetections, maxAvgConfidence).
		Order("avg_confidence ASC")

	var results []PhantomSpecies
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("phantom species query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetDawnChorusRaw(ctx context.Context, since int64, startHour, endHour, tzOffsetSeconds int, modelID *uint) ([]DawnChorusRawEntry, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	// Hour filter and date grouping MUST share the same offset to keep the query internally
	// consistent (otherwise a near-midnight detection could be hour-filtered in one zone and
	// date-grouped in another).
	hourExpr := r.hourFromUnixExpr("d.detected_at", tzOffsetSeconds)
	dateExpr := r.dateFromUnixExpr("d.detected_at", tzOffsetSeconds)

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, %s as date, MIN(d.detected_at) as earliest_at",
			lab, dateExpr)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where("d.detected_at >= ?", since).
		Where(fpWhere).
		Where(fmt.Sprintf("%s >= ? AND %s < ?", hourExpr, hourExpr), startHour, endHour)

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	query = query.Group(fmt.Sprintf("%s.scientific_name, %s", lab, dateExpr))

	var results []DawnChorusRawEntry
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("dawn chorus query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetNewArrivals(ctx context.Context, recentSince int64, modelID *uint) ([]NewArrival, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, MIN(d.detected_at) as first_detected, COUNT(*) as detection_count", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where(fpWhere)

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	query = query.Group(lab+".scientific_name").
		Having("MIN(d.detected_at) >= ?", recentSince).
		Order("first_detected DESC")

	var results []NewArrival
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("new arrivals query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetGoneQuiet(ctx context.Context, recentSince int64, minTotalDetections int, modelID *uint) ([]GoneQuietSpecies, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, MAX(d.detected_at) as last_detected, COUNT(*) as total_detections", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where(fpWhere)

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	query = query.Group(lab+".scientific_name").
		Having("COUNT(*) >= ? AND MAX(d.detected_at) < ?", minTotalDetections, recentSince).
		Order("last_detected DESC")

	var results []GoneQuietSpecies
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("gone quiet query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetDashboardKPIs(ctx context.Context, todaySince int64, tzOffsetSeconds int, modelID *uint) (*DashboardKPIs, error) {
	det := r.detectionsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	dateExpr := r.dateFromUnixExpr("d.detected_at", tzOffsetSeconds)

	kpis := &DashboardKPIs{}

	baseQuery := func() *gorm.DB {
		q := r.db.WithContext(ctx).Table(fmt.Sprintf("%s d", det)).
			Joins(fpJoin).Where(fpWhere)
		if modelID != nil {
			q = q.Where("d.model_id = ?", *modelID)
		}
		return q
	}

	// 1. Lifetime species (join labels to count distinct scientific names across models)
	lab := r.labelsTable()
	if err := baseQuery().
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Select(fmt.Sprintf("COUNT(DISTINCT %s.scientific_name)", lab)).
		Scan(&kpis.LifetimeSpecies).Error; err != nil {
		return nil, fmt.Errorf("lifetime species: %w", err)
	}

	// 2. Today's detections
	if err := baseQuery().Where("d.detected_at >= ?", todaySince).
		Select("COUNT(*)").Scan(&kpis.TodayDetections).Error; err != nil {
		return nil, fmt.Errorf("today detections: %w", err)
	}

	// 3. Best day (scoped to last year for performance)
	oneYearAgo := time.Unix(todaySince, 0).AddDate(-1, 0, 0).Unix()
	var bestDay struct {
		Date  string
		Count int64
	}
	if err := baseQuery().
		Where("d.detected_at >= ?", oneYearAgo).
		Select(fmt.Sprintf("%s as date, COUNT(*) as count", dateExpr)).
		Group("date").Order("count DESC").Limit(1).
		Scan(&bestDay).Error; err != nil {
		return nil, fmt.Errorf("best day: %w", err)
	}
	kpis.BestDayDate = bestDay.Date
	kpis.BestDayCount = bestDay.Count

	// 4. Recent distinct dates (for streak calculation in caller)
	if err := baseQuery().
		Select(fmt.Sprintf("DISTINCT %s as date", dateExpr)).
		Order("date DESC").Limit(90).
		Pluck("date", &kpis.RecentDates).Error; err != nil {
		return nil, fmt.Errorf("recent dates: %w", err)
	}

	return kpis, nil
}
