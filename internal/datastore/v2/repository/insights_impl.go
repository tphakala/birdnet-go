package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
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

// Table name helpers, same pattern as detectionRepository.
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

func (r *insightsRepository) modelContributionsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionModelContributions
	}
	return tableDetectionModelContributions
}

func (r *insightsRepository) modelsTable() string {
	if r.useV2Prefix {
		return tableV2AIModels
	}
	return tableAIModels
}

// SQL expression helpers, same offset-arithmetic pattern as detectionRepository.hourFromUnixExpr.
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

// hasContributionStatsSQL is the one predicate that decides whether a contribution
// row has recorded confidence stats. The four stat columns are written together,
// but nothing constrains that, so min and sum both gate every stats expression:
// a row can then never feed the minimum without feeding the mean, or the reverse.
const hasContributionStatsSQL = "c.confidence_sum IS NOT NULL AND c.min_confidence IS NOT NULL"

// modelContributionRow is one scanned row of the per-model aggregate query.
type modelContributionRow struct {
	ModelID             uint
	Name                string
	Version             string
	Variant             string
	ModelType           string
	DetectionCount      int64
	Hits                int64
	SoleDetections      int64
	MaxConfidence       float64
	MinConfidence       *float64
	ConfidenceSum       *float64
	StatsHits           int64
	DetectionsWithStats int64
	Correct             int64
	FalsePositive       int64
}

// modelContributionTotalsRow is the scanned row of the range totals query.
type modelContributionTotalsRow struct {
	TotalDetections             int64
	DetectionsWithContributions int64
}

// GetModelContributionStats implements InsightsRepository. The three statements
// run in one read transaction so they see one snapshot.
func (r *insightsRepository) GetModelContributionStats(ctx context.Context, start, end int64) (*ModelContributionStats, error) {
	result := &ModelContributionStats{
		Models:     []ModelContributionStat{},
		Agreements: []ModelAgreement{},
	}
	if start >= end {
		return result, nil
	}

	// Every query below must use tx, never r.db: SQLite runs with one open
	// connection, so a query on r.db would wait for the transaction's connection.
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := r.queryModelContributionRows(tx, start, end)
		if err != nil {
			return err
		}
		agreements, err := r.queryModelAgreements(tx, start, end)
		if err != nil {
			return err
		}
		totals, err := r.queryContributionTotals(tx, start, end)
		if err != nil {
			return err
		}
		result = assembleModelContributionStats(rows, agreements, totals)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("model contribution stats: %w", err)
	}
	return result, nil
}

// queryModelContributionRows runs the per-model aggregate. The derived table pd
// counts the contributing models per detection over the same range, which gives
// the sole-contributor count without a subquery inside an aggregate.
func (r *insightsRepository) queryModelContributionRows(tx *gorm.DB, start, end int64) ([]modelContributionRow, error) {
	contrib := r.modelContributionsTable()
	query := fmt.Sprintf(`
SELECT c.model_id AS model_id, m.name AS name, m.version AS version, m.variant AS variant, m.model_type AS model_type,
  COUNT(*) AS detection_count,
  SUM(c.hit_count) AS hits,
  SUM(CASE WHEN pd.model_count = 1 THEN 1 ELSE 0 END) AS sole_detections,
  MAX(c.max_confidence) AS max_confidence,
  MIN(CASE WHEN %[1]s THEN c.min_confidence END) AS min_confidence,
  SUM(CASE WHEN %[1]s THEN c.confidence_sum END) AS confidence_sum,
  SUM(CASE WHEN %[1]s THEN c.hit_count ELSE 0 END) AS stats_hits,
  SUM(CASE WHEN %[1]s THEN 1 ELSE 0 END) AS detections_with_stats,
  SUM(CASE WHEN dr.verified = ? THEN 1 ELSE 0 END) AS correct,
  SUM(CASE WHEN dr.verified = ? THEN 1 ELSE 0 END) AS false_positive
FROM %[2]s c
JOIN %[3]s d ON d.id = c.detection_id
JOIN (SELECT c2.detection_id AS detection_id, COUNT(*) AS model_count
      FROM %[2]s c2 JOIN %[3]s d2 ON d2.id = c2.detection_id
      WHERE d2.detected_at >= ? AND d2.detected_at < ?
      GROUP BY c2.detection_id) pd ON pd.detection_id = c.detection_id
JOIN %[4]s m ON m.id = c.model_id
LEFT JOIN %[5]s dr ON dr.detection_id = c.detection_id
WHERE d.detected_at >= ? AND d.detected_at < ?
GROUP BY c.model_id, m.name, m.version, m.variant, m.model_type
ORDER BY detection_count DESC, c.model_id ASC`,
		hasContributionStatsSQL, contrib, r.detectionsTable(), r.modelsTable(), r.reviewsTable())

	var rows []modelContributionRow
	// Args in text order: the two verdicts of the CASE expressions, then the
	// derived table's range, then the outer WHERE range.
	err := tx.Raw(query,
		entities.VerificationCorrect, entities.VerificationFalsePositive,
		start, end,
		start, end,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("per-model rows: %w", err)
	}
	return rows, nil
}

// queryModelAgreements counts the detections each model pair shares. The join
// condition c2.model_id > c1.model_id yields each pair once with model_a < model_b.
func (r *insightsRepository) queryModelAgreements(tx *gorm.DB, start, end int64) ([]ModelAgreement, error) {
	query := fmt.Sprintf(`
SELECT c1.model_id AS model_a_id, c2.model_id AS model_b_id, COUNT(*) AS shared_detections
FROM %[1]s c1
JOIN %[1]s c2 ON c2.detection_id = c1.detection_id AND c2.model_id > c1.model_id
JOIN %[2]s d ON d.id = c1.detection_id
WHERE d.detected_at >= ? AND d.detected_at < ?
GROUP BY c1.model_id, c2.model_id
ORDER BY shared_detections DESC, model_a_id ASC, model_b_id ASC`,
		r.modelContributionsTable(), r.detectionsTable())

	var agreements []ModelAgreement
	if err := tx.Raw(query, start, end).Scan(&agreements).Error; err != nil {
		return nil, fmt.Errorf("agreements: %w", err)
	}
	return agreements, nil
}

// queryContributionTotals counts the detections in range and those with at least
// one contribution row.
func (r *insightsRepository) queryContributionTotals(tx *gorm.DB, start, end int64) (modelContributionTotalsRow, error) {
	query := fmt.Sprintf(`
SELECT COUNT(DISTINCT d.id) AS total_detections, COUNT(DISTINCT c.detection_id) AS detections_with_contributions
FROM %s d LEFT JOIN %s c ON c.detection_id = d.id
WHERE d.detected_at >= ? AND d.detected_at < ?`,
		r.detectionsTable(), r.modelContributionsTable())

	var totals modelContributionTotalsRow
	if err := tx.Raw(query, start, end).Scan(&totals).Error; err != nil {
		return totals, fmt.Errorf("totals: %w", err)
	}
	return totals, nil
}

// assembleModelContributionStats derives hit shares, means and outcome counts in
// Go. Unreviewed is the remainder after correct and false positive, so an empty
// or legacy verdict counts as unreviewed instead of falling out of every bucket.
func assembleModelContributionStats(rows []modelContributionRow, agreements []ModelAgreement, totals modelContributionTotalsRow) *ModelContributionStats {
	stats := &ModelContributionStats{
		TotalDetections:             totals.TotalDetections,
		DetectionsWithContributions: totals.DetectionsWithContributions,
		Models:                      make([]ModelContributionStat, 0, len(rows)),
		Agreements:                  make([]ModelAgreement, 0, len(agreements)),
	}
	for i := range rows {
		stats.TotalHits += rows[i].Hits
	}
	for i := range rows {
		row := &rows[i]
		stat := ModelContributionStat{
			ModelID:             row.ModelID,
			Name:                row.Name,
			Version:             row.Version,
			Variant:             row.Variant,
			ModelType:           row.ModelType,
			Detections:          row.DetectionCount,
			Hits:                row.Hits,
			SoleDetections:      row.SoleDetections,
			DetectionsWithStats: row.DetectionsWithStats,
			MaxConfidence:       row.MaxConfidence,
			MinConfidence:       row.MinConfidence,
			Correct:             row.Correct,
			FalsePositive:       row.FalsePositive,
			Unreviewed:          row.DetectionCount - row.Correct - row.FalsePositive,
		}
		if stats.TotalHits > 0 {
			stat.HitShare = float64(row.Hits) / float64(stats.TotalHits)
		}
		if row.StatsHits > 0 && row.ConfidenceSum != nil {
			mean := *row.ConfidenceSum / float64(row.StatsHits)
			if row.MinConfidence != nil {
				// Clamp float rounding so min <= mean <= max always holds.
				mean = min(max(mean, *row.MinConfidence), row.MaxConfidence)
			}
			stat.MeanConfidence = &mean
		}
		stats.Models = append(stats.Models, stat)
	}
	stats.Agreements = append(stats.Agreements, agreements...)
	return stats
}
