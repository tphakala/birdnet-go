package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/wallclock"
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

// hitContributionsTable is the contribution table restricted to rows with a hit.
// A row with hit_count 0 records a model that was active on the source without a
// hit; the contribution statistics select from this relation, so they count hits
// only and a new query gets the filter by construction.
func (r *insightsRepository) hitContributionsTable() string {
	return "(SELECT * FROM " + r.modelContributionsTable() + " WHERE hit_count > 0)"
}

func (r *insightsRepository) modelsTable() string {
	if r.useV2Prefix {
		return tableV2AIModels
	}
	return tableAIModels
}

// yearRangesSpan returns the smallest Unix-seconds span that contains every range.
func yearRangesSpan(ranges []TimeRange) (start, end int64) {
	start, end = ranges[0].Start, ranges[0].End
	for _, tr := range ranges[1:] {
		start = min(start, tr.Start)
		end = max(end, tr.End)
	}
	return start, end
}

// dateOneYearBefore returns the same month and day one year earlier; 29 February becomes
// 1 March once the date is resolved, as time.Date normalises it (wallclock.DayBounds does).
func dateOneYearBefore(d wallclock.Date) wallclock.Date {
	return wallclock.Date{Year: d.Year - 1, Month: d.Month, Day: d.Day}
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

func (r *insightsRepository) GetExpectedSpeciesToday(ctx context.Context, yearRanges []TimeRange, loc *time.Location, modelID *uint) ([]ExpectedSpecies, error) {
	if len(yearRanges) == 0 {
		return nil, nil
	}

	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")

	// The ranges are disjoint stretches of a few days, but one DST change inside the covered
	// span is enough to give rows different offsets, so the buckets follow each row's own.
	spanStart, spanEnd := yearRangesSpan(yearRanges)
	now := time.Now()
	from, to := wallclock.Span(spanStart, spanEnd, now)
	dateExpr := WallClockDateExpr("d.detected_at", loc, from, to, r.isMySQL)
	yearExpr := wallClockYearSQL("d.detected_at", perRowOffsetSQL("d.detected_at", loc, spanStart, spanEnd, now), r.isMySQL)

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
		orScope = orScope.Or("d.detected_at >= ? AND d.detected_at < ?", tr.Start, tr.End)
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

// dawnChorusKey identifies one merged dawn chorus row: a species on a local calendar date.
type dawnChorusKey struct {
	scientificName string
	date           string
}

func (r *insightsRepository) GetDawnChorusRaw(ctx context.Context, since, until int64, startHour, endHour int, loc *time.Location, modelID *uint) ([]DawnChorusRawEntry, error) {
	// The enumeration is bounded by wallclock.Span whatever the caller passes, so the number of
	// queries cannot grow with the span; the first and last segment are stretched to since and
	// until, so rows outside the bounded span use the offset of the nearest segment.
	segments := wallclock.CoveringSegments(loc, since, until, time.Now())
	if len(segments) == 0 {
		// until <= since: CoveringSegments returns none, but the contract keeps every detection
		// from since on, so use one open segment at the offset in effect at since.
		segments = []wallclock.Segment{{Start: since, End: since, OffsetSeconds: wallclock.OffsetAt(loc, time.Unix(since, 0))}}
	}

	merged := make([]DawnChorusRawEntry, 0)
	index := make(map[dawnChorusKey]int)
	for i, seg := range segments {
		// The last segment is open-ended so detections stamped after until are still counted.
		rows, err := r.dawnChorusSegmentRows(ctx, seg, i == len(segments)-1, startHour, endHour, modelID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := dawnChorusKey{scientificName: row.ScientificName, date: row.Date}
			if at, ok := index[key]; ok {
				// A local date can straddle an offset change, so the same species and
				// date can come back from two segments: keep the earliest.
				merged[at].EarliestAt = min(merged[at].EarliestAt, row.EarliestAt)
				continue
			}
			index[key] = len(merged)
			merged = append(merged, row)
		}
	}
	return merged, nil
}

// dawnChorusSegmentRows runs the dawn chorus query over one zone segment. The segment's
// constant UTC offset is applied to both the hour filter and the date grouping, so they
// agree on every row. The upper bound seg.End is skipped when last is true.
func (r *insightsRepository) dawnChorusSegmentRows(ctx context.Context, seg wallclock.Segment, last bool, startHour, endHour int, modelID *uint) ([]DawnChorusRawEntry, error) {
	det := r.detectionsTable()
	lab := r.labelsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")
	offset := segmentOffsetSQL(seg)
	hourExpr := wallClockHourSQL("d.detected_at", offset, r.isMySQL)
	dateExpr := wallClockDateSQL("d.detected_at", offset, r.isMySQL)

	query := r.db.WithContext(ctx).
		Table(fmt.Sprintf("%s d", det)).
		Select(fmt.Sprintf("%s.scientific_name, %s as date, MIN(d.detected_at) as earliest_at",
			lab, dateExpr)).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = d.label_id", lab, lab)).
		Joins(fpJoin).
		Where("d.detected_at >= ?", seg.Start).
		Where(fpWhere).
		Where(fmt.Sprintf("%s >= ? AND %s < ?", hourExpr, hourExpr), startHour, endHour)

	if !last {
		query = query.Where("d.detected_at < ?", seg.End)
	}
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

func (r *insightsRepository) GetDashboardKPIs(ctx context.Context, todaySince int64, loc *time.Location, modelID *uint) (*DashboardKPIs, error) {
	det := r.detectionsTable()
	fpJoin, fpWhere := r.falsePositiveExclusion("d")

	// Best day and recent dates bucket by each row's own offset over the year before today. Older
	// rows use the offset at the start of that year; they are outside the best-day window and past
	// the recent dates the streak reads.
	if loc == nil {
		loc = time.UTC
	}
	oneYearAgo, _ := wallclock.DayBounds(dateOneYearBefore(wallclock.DateOf(time.Unix(todaySince, 0).In(loc))), loc)
	now := time.Now()
	from, to := wallclock.Span(oneYearAgo.Unix(), now.Unix(), now)
	dateExpr := WallClockDateExpr("d.detected_at", loc, from, to, r.isMySQL)

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
	var bestDay struct {
		Date  string
		Count int64
	}
	if err := baseQuery().
		Where("d.detected_at >= ?", oneYearAgo.Unix()).
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

// queryModelContributionRows runs the per-model aggregate over hit rows. The
// derived table pd counts the models with a hit per detection over the same
// range, which gives the sole-contributor count without a subquery inside an
// aggregate.
func (r *insightsRepository) queryModelContributionRows(tx *gorm.DB, start, end int64) ([]modelContributionRow, error) {
	contrib := r.hitContributionsTable()
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

// queryModelAgreements counts the detections in which both models of a pair have
// a hit. The join condition c2.model_id > c1.model_id yields each pair once with
// model_a < model_b.
func (r *insightsRepository) queryModelAgreements(tx *gorm.DB, start, end int64) ([]ModelAgreement, error) {
	query := fmt.Sprintf(`
SELECT c1.model_id AS model_a_id, c2.model_id AS model_b_id, COUNT(*) AS shared_detections
FROM %[1]s c1
JOIN %[1]s c2 ON c2.detection_id = c1.detection_id AND c2.model_id > c1.model_id
JOIN %[2]s d ON d.id = c1.detection_id
WHERE d.detected_at >= ? AND d.detected_at < ?
GROUP BY c1.model_id, c2.model_id
ORDER BY shared_detections DESC, model_a_id ASC, model_b_id ASC`,
		r.hitContributionsTable(), r.detectionsTable())

	var agreements []ModelAgreement
	if err := tx.Raw(query, start, end).Scan(&agreements).Error; err != nil {
		return nil, fmt.Errorf("agreements: %w", err)
	}
	return agreements, nil
}

// queryContributionTotals counts the detections in range and those with at least
// one hit row. The LEFT JOIN keeps detections without one in total_detections.
func (r *insightsRepository) queryContributionTotals(tx *gorm.DB, start, end int64) (modelContributionTotalsRow, error) {
	query := fmt.Sprintf(`
SELECT COUNT(DISTINCT d.id) AS total_detections, COUNT(DISTINCT c.detection_id) AS detections_with_contributions
FROM %s d LEFT JOIN %s c ON c.detection_id = d.id
WHERE d.detected_at >= ? AND d.detected_at < ?`,
		r.detectionsTable(), r.hitContributionsTable())

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
