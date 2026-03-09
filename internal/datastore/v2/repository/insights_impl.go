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

// Stub implementations (filled in subsequent tasks)

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

func (r *insightsRepository) GetNewArrivals(_ context.Context, _ int64, _ *uint) ([]NewArrival, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *insightsRepository) GetGoneQuiet(_ context.Context, _ int64, _ int, _ *uint) ([]GoneQuietSpecies, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *insightsRepository) GetDashboardKPIs(_ context.Context, _ int64, _ *uint) (*DashboardKPIs, error) {
	return nil, fmt.Errorf("not implemented")
}
