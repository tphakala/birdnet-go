package repository

import (
	"context"
	"time"
)

// InsightsRepository provides analytical queries for the Insights page.
// Separated from DetectionRepository to keep interfaces focused.
type InsightsRepository interface {
	// GetExpectedSpeciesToday returns species detected within the given
	// Unix timestamp ranges (one per previous year, pre-calculated for index usage).
	// tzOffsetSeconds is the configured timezone's UTC offset, applied so the date and
	// year grouping bucket by wall-clock value in that zone rather than the DB/OS-local zone.
	GetExpectedSpeciesToday(ctx context.Context, yearRanges []TimeRange, tzOffsetSeconds int, modelID *uint) ([]ExpectedSpecies, error)

	// GetPhantomSpecies returns species with minDetections+ in the period
	// but average confidence below maxAvgConfidence.
	GetPhantomSpecies(ctx context.Context, since int64, minDetections int, maxAvgConfidence float64, modelID *uint) ([]PhantomSpecies, error)

	// GetDawnChorusRaw returns per-day earliest detection per species
	// in the given hour range. Caller averages time-of-day in Go.
	// The hour filter and the date grouping both use the wall clock of loc (time.Local when
	// nil): the UTC offset in effect at each detection inside [since, until), so a DST change
	// inside the period does not shift the window. The span is split at loc's offset changes
	// (see ZoneOffsetSegments) and the results are merged to one entry per species and date.
	// until only bounds that split; detections at or after it are still included (they fall in
	// the last segment), so coverage is every detection with detected_at >= since. Those
	// detections use the offset in effect at the start of the last segment, which is the
	// offset at since when until <= since. The work grows with the number of offset changes in
	// the span (one query each), so callers must keep [since, until) to a bounded period.
	GetDawnChorusRaw(ctx context.Context, since, until int64, startHour, endHour int, loc *time.Location, modelID *uint) ([]DawnChorusRawEntry, error)

	// GetNewArrivals returns species whose first-ever detection is after recentSince.
	GetNewArrivals(ctx context.Context, recentSince int64, modelID *uint) ([]NewArrival, error)

	// GetGoneQuiet returns species with minTotalDetections+ total
	// but no detection after recentSince.
	GetGoneQuiet(ctx context.Context, recentSince int64, minTotalDetections int, modelID *uint) ([]GoneQuietSpecies, error)

	// GetDashboardKPIs returns lifetime species count, today's detections,
	// best day, and recent distinct dates for streak calculation.
	// tzOffsetSeconds is the configured timezone's UTC offset, applied so the date grouping
	// (best day, recent dates) buckets by wall-clock date in that zone rather than the
	// DB/OS-local zone.
	GetDashboardKPIs(ctx context.Context, todaySince int64, tzOffsetSeconds int, modelID *uint) (*DashboardKPIs, error)

	// GetModelContributionStats returns per-model statistics read from
	// detection_model_contributions for detections whose detected_at lies in the
	// half-open Unix-seconds range [start, end). Reviews of every verdict are
	// included (false positives are not excluded), so outcomes can be counted per
	// model. The result is never nil on success and its slices are never nil.
	GetModelContributionStats(ctx context.Context, start, end int64) (*ModelContributionStats, error)
}
