package v2only

import (
	"context"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// Range analytics bucket each detection by the wall clock in ds.timezone at the detection's own
// instant. The repository queries add one fixed UTC offset to detected_at, so a range that
// contains an offset change is split into segments of constant offset and the per-segment
// results are summed (counts are additive), while queries that cannot be merged (distinct counts,
// pagination) use a per-row CASE offset in SQL instead, see dateExpr.

// zoneSegments splits [start, end) at the offset changes of the configured timezone, see
// wallclock.CoveringSegments: the segments tile [start, end) exactly, so every row of the range
// falls in exactly one segment, and rows outside the bounded span use the offset of the nearest
// segment, the same rule dateExpr applies. A range with an open start costs one query per offset
// period since 1970 in the callers.
func (ds *Datastore) zoneSegments(start, end int64) []wallclock.Segment {
	return wallclock.CoveringSegments(ds.timezone, start, end, time.Now())
}

// dateExpr returns the SQL expression for the local calendar date of d.detected_at over rows in
// [start, end), see repository.WallClockDateExpr.
func (ds *Datastore) dateExpr(start, end int64) string {
	from, to := wallclock.Span(start, end, time.Now())
	return repository.WallClockDateExpr("d.detected_at", ds.timezone, from, to, ds.manager.IsMySQL())
}

// hourlyCountsByZone returns the number of detections in each local hour of [start, end) for the
// labels (all labels when empty).
func (ds *Datastore) hourlyCountsByZone(ctx context.Context, start, end int64, labelIDs []uint) ([hoursPerDay]int, error) {
	var counts [hoursPerDay]int
	for _, seg := range ds.zoneSegments(start, end) {
		part, err := ds.detection.GetHourlyDistribution(ctx, seg.Start, seg.End, seg.OffsetSeconds, labelIDs, nil)
		if err != nil {
			return counts, err
		}
		for _, row := range part {
			if row.Hour >= 0 && row.Hour < hoursPerDay {
				counts[row.Hour] += int(row.Count)
			}
		}
	}
	return counts, nil
}

// hourlyRows lists the non-empty hours of counts in ascending order, the shape the repository
// returns for one query.
func hourlyRows(counts *[hoursPerDay]int) []datastore.HourlyDistributionData {
	rows := make([]datastore.HourlyDistributionData, 0, hoursPerDay)
	for hour, count := range counts {
		if count > 0 {
			rows = append(rows, datastore.HourlyDistributionData{Hour: hour, Count: count})
		}
	}
	return rows
}

// batchHourlyByZone returns, per label, the number of detections in each local hour of
// [start, end). Repository errors are returned unwrapped.
func (ds *Datastore) batchHourlyByZone(ctx context.Context, labelIDs []uint, start, end int64, minConfidence float64) (map[uint][hoursPerDay]int, error) {
	hourlyByLabel := make(map[uint][hoursPerDay]int, len(labelIDs))
	for _, seg := range ds.zoneSegments(start, end) {
		part, err := ds.detection.GetBatchHourlyOccurrences(ctx, labelIDs, seg.Start, seg.End, seg.OffsetSeconds, minConfidence)
		if err != nil {
			return nil, err
		}
		// Range over keys only to avoid copying the 192-byte [24]int value on every iteration.
		for labelID := range part {
			hours := part[labelID]
			acc := hourlyByLabel[labelID]
			for h := range hoursPerDay {
				acc[h] += hours[h]
			}
			hourlyByLabel[labelID] = acc
		}
	}
	return hourlyByLabel, nil
}

// dailyCountsByZone returns the number of detections on each local date of [start, end) for the
// labels (all labels when empty), newest date first. Only the detection counts are returned: the
// distinct species and average confidence of the repository rows cannot be merged across
// segments.
func (ds *Datastore) dailyCountsByZone(ctx context.Context, start, end int64, labelIDs []uint) ([]datastore.DailyAnalyticsData, error) {
	counts := map[string]int{}
	for _, seg := range ds.zoneSegments(start, end) {
		part, err := ds.detection.GetDailyAnalytics(ctx, seg.Start, seg.End, seg.OffsetSeconds, labelIDs, nil)
		if err != nil {
			return nil, err
		}
		for _, row := range part {
			counts[row.Date] += int(row.TotalDetections)
		}
	}
	result := make([]datastore.DailyAnalyticsData, 0, len(counts))
	for date, count := range counts {
		result = append(result, datastore.DailyAnalyticsData{Date: date, Count: count})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Date > result[j].Date })
	return result, nil
}
