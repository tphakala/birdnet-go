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

func (r *insightsRepository) GetExpectedSpeciesToday(_ context.Context, _ []TimeRange, _ *uint) ([]ExpectedSpecies, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *insightsRepository) GetPhantomSpecies(_ context.Context, _ int64, _ int, _ float64, _ *uint) ([]PhantomSpecies, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *insightsRepository) GetDawnChorusRaw(_ context.Context, _ int64, _, _ int, _ *uint) ([]DawnChorusRawEntry, error) {
	return nil, fmt.Errorf("not implemented")
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
