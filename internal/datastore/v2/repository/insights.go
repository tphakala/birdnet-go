package repository

import (
	"context"
	"time"
)

// InsightsRepository provides analytical queries for the Insights page.
// Separated from DetectionRepository to keep interfaces focused.
type InsightsRepository interface {
	// GetExpectedSpeciesToday returns species detected within the given
	// half-open Unix timestamp ranges (one per previous year, pre-calculated for index usage).
	// The date and year grouping bucket by the wall clock of loc (UTC when nil), not the
	// DB/OS-local zone: every row is shifted by the UTC offset in effect at its own instant, so
	// a DST change inside the ranges does not move a row onto the wrong date or year.
	GetExpectedSpeciesToday(ctx context.Context, yearRanges []TimeRange, loc *time.Location, modelID *uint) ([]ExpectedSpecies, error)

	// GetPhantomSpecies returns species with minDetections+ in the period
	// but average confidence below maxAvgConfidence.
	GetPhantomSpecies(ctx context.Context, since int64, minDetections int, maxAvgConfidence float64, modelID *uint) ([]PhantomSpecies, error)

	// GetDawnChorusRaw returns per-day earliest detection per species
	// in the given hour range. Caller averages time-of-day in Go.
	// The hour filter and the date grouping both use the wall clock of loc (time.Local when
	// nil): the UTC offset in effect at each detection inside [since, until), so a DST change
	// inside the period does not shift the window. The span is split at loc's offset changes
	// (see wallclock.CoveringSegments) and the results are merged to one entry per species and date.
	// until only bounds that split; detections at or after it are still included (they fall in
	// the last segment), so coverage is every detection with detected_at >= since. Those
	// detections use the offset in effect at the start of the last segment, which is the
	// offset at since when until <= since. The work grows with the number of offset changes in
	// the span (one query each). The split is bounded by wallclock.Span, from the Unix epoch to
	// one leap year past now, whatever since and until are; rows outside that span use the offset
	// of the nearest segment.
	GetDawnChorusRaw(ctx context.Context, since, until int64, startHour, endHour int, loc *time.Location, modelID *uint) ([]DawnChorusRawEntry, error)

	// GetNewArrivals returns species whose first-ever detection is after recentSince.
	GetNewArrivals(ctx context.Context, recentSince int64, modelID *uint) ([]NewArrival, error)

	// GetGoneQuiet returns species with minTotalDetections+ total
	// but no detection after recentSince.
	GetGoneQuiet(ctx context.Context, recentSince int64, minTotalDetections int, modelID *uint) ([]GoneQuietSpecies, error)

	// GetDashboardKPIs returns lifetime species count, today's detections,
	// best day, and recent distinct dates for streak calculation.
	// todaySince is the first instant of the local day in loc. The date grouping (best day,
	// recent dates) buckets by the wall-clock date of loc (UTC when nil), shifting every row by
	// the UTC offset in effect at its own instant, rather than the DB/OS-local zone. The best
	// day covers the year before todaySince's local date; rows older than the start of that
	// window (only the recent-dates list can reach them) take the offset in effect at its start.
	GetDashboardKPIs(ctx context.Context, todaySince int64, loc *time.Location, modelID *uint) (*DashboardKPIs, error)

	// GetModelContributionStats returns per-model statistics read from
	// detection_model_contributions for detections whose detected_at lies in the
	// half-open Unix-seconds range [start, end). Reviews of every verdict are
	// included (false positives are not excluded), so outcomes can be counted per
	// model. Only contribution rows with a hit count above 0 are counted; rows
	// recording a model that was active without a hit are ignored. The result is
	// never nil on success and its slices are never nil.
	GetModelContributionStats(ctx context.Context, start, end int64) (*ModelContributionStats, error)
}
