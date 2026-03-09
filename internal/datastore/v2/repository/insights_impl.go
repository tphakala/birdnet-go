package repository

import (
	"context"
	"fmt"

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

// SQL expression helpers — same pattern as detectionRepository.
func (r *insightsRepository) dateFromUnixExpr(column string) string {
	if r.isMySQL {
		return fmt.Sprintf("DATE(FROM_UNIXTIME(%s))", column)
	}
	return fmt.Sprintf("DATE(datetime(%s, 'unixepoch', 'localtime'))", column)
}

func (r *insightsRepository) hourFromUnixExpr(column string) string {
	if r.isMySQL {
		return fmt.Sprintf("HOUR(FROM_UNIXTIME(%s))", column)
	}
	return fmt.Sprintf("CAST(strftime('%%H', datetime(%s, 'unixepoch', 'localtime')) AS INTEGER)", column)
}

func (r *insightsRepository) yearFromUnixExpr(column string) string {
	if r.isMySQL {
		return fmt.Sprintf("YEAR(FROM_UNIXTIME(%s))", column)
	}
	return fmt.Sprintf("CAST(strftime('%%Y', datetime(%s, 'unixepoch', 'localtime')) AS INTEGER)", column)
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

func (r *insightsRepository) GetExpectedSpeciesToday(ctx context.Context, yearRanges []TimeRange, modelID *uint) ([]ExpectedSpecies, error) {
	if len(yearRanges) == 0 {
		return nil, nil
	}

	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	dateExpr := r.dateFromUnixExpr("d.detected_at")
	yearExpr := r.yearFromUnixExpr("d.detected_at")

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("d.label_id, %s.scientific_name, COUNT(DISTINCT %s) as years_seen, MAX(%s) as last_seen_date",
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

	query = query.Group("d.label_id").Order("years_seen DESC")

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
		Select(fmt.Sprintf("d.label_id, %s.scientific_name, COUNT(*) as detection_count, AVG(d.confidence) as avg_confidence, MAX(d.confidence) as max_confidence", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where("d.detected_at >= ?", since).
		Where(fpWhere).
		Group("d.label_id").
		Having("COUNT(*) >= ? AND AVG(d.confidence) < ?", minDetections, maxAvgConfidence).
		Order("avg_confidence ASC")

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	var results []PhantomSpecies
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("phantom species query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetDawnChorusRaw(ctx context.Context, since int64, startHour, endHour int, modelID *uint) ([]DawnChorusRawEntry, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	hourExpr := r.hourFromUnixExpr("d.detected_at")
	dateExpr := r.dateFromUnixExpr("d.detected_at")

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("d.label_id, %s.scientific_name, %s as date, MIN(d.detected_at) as earliest_at",
			lab, dateExpr)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where("d.detected_at >= ?", since).
		Where(fpWhere).
		Where(fmt.Sprintf("%s >= ? AND %s < ?", hourExpr, hourExpr), startHour, endHour).
		Group(fmt.Sprintf("d.label_id, %s", dateExpr))

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

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
		Select(fmt.Sprintf("d.label_id, %s.scientific_name, MIN(d.detected_at) as first_detected, COUNT(*) as detection_count", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where(fpWhere).
		Group("d.label_id").
		Having("MIN(d.detected_at) >= ?", recentSince).
		Order("first_detected DESC")

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

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
		Select(fmt.Sprintf("d.label_id, %s.scientific_name, MAX(d.detected_at) as last_detected, COUNT(*) as total_detections", lab)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where(fpWhere).
		Group("d.label_id").
		Having("COUNT(*) >= ? AND MAX(d.detected_at) < ?", minTotalDetections, recentSince).
		Order("last_detected DESC")

	if modelID != nil {
		query = query.Where("d.model_id = ?", *modelID)
	}

	var results []GoneQuietSpecies
	if err := query.Scan(&results).Error; err != nil {
		return nil, fmt.Errorf("gone quiet query: %w", err)
	}
	return results, nil
}

func (r *insightsRepository) GetDashboardKPIs(ctx context.Context, todaySince int64, modelID *uint) (*DashboardKPIs, error) {
	det := r.detectionsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	dateExpr := r.dateFromUnixExpr("d.detected_at")

	kpis := &DashboardKPIs{}

	baseQuery := func() *gorm.DB {
		q := r.db.WithContext(ctx).Table(fmt.Sprintf("%s d", det)).
			Joins(fpJoin).Where(fpWhere)
		if modelID != nil {
			q = q.Where("d.model_id = ?", *modelID)
		}
		return q
	}

	// 1. Lifetime species
	if err := baseQuery().Select("COUNT(DISTINCT d.label_id)").Scan(&kpis.LifetimeSpecies).Error; err != nil {
		return nil, fmt.Errorf("lifetime species: %w", err)
	}

	// 2. Today's detections
	if err := baseQuery().Where("d.detected_at >= ?", todaySince).
		Select("COUNT(*)").Scan(&kpis.TodayDetections).Error; err != nil {
		return nil, fmt.Errorf("today detections: %w", err)
	}

	// 3. Best day (scoped to last 365 days for performance)
	oneYearAgo := todaySince - 365*24*60*60
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
