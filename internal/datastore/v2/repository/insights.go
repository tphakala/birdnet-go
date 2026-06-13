package repository

import "context"

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
	// tzOffsetSeconds is the configured timezone's UTC offset, applied to BOTH the hour
	// filter and the date grouping so the query is internally consistent (a single offset
	// for both) rather than mixing the DB/OS-local zone with the configured zone.
	GetDawnChorusRaw(ctx context.Context, since int64, startHour, endHour, tzOffsetSeconds int, modelID *uint) ([]DawnChorusRawEntry, error)

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
}
