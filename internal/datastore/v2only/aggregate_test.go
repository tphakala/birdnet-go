package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// hours builds a [24]int from (hour, count) pairs for terse test fixtures.
func hours(pairs ...[2]int) [24]int {
	var h [24]int
	for _, p := range pairs {
		h[p[0]] = p[1]
	}
	return h
}

// bucketSum sums a species' normalized buckets (should be ~1.0 for any non-empty species).
// Takes a pointer to avoid copying the 192-byte array (gocritic hugeParam).
func bucketSum(b *[24]float64) float64 {
	sum := 0.0
	for _, v := range b {
		sum += v
	}
	return sum
}

func TestBuildSpeciesHourlyDistribution_NormalizesAndOrders(t *testing.T) {
	t.Parallel()

	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 100},
		{LabelID: 2, ScientificName: "Erithacus rubecula", Count: 50},
		{LabelID: 3, ScientificName: "Strix aluco", Count: 10},
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{0, 2}, [2]int{12, 6}, [2]int{18, 2}), // total 10
		2: hours([2]int{6, 4}, [2]int{7, 1}),                 // total 5
		3: hours([2]int{23, 1}),                              // total 1
	}

	got := buildSpeciesHourlyDistribution(top, hourlyByLabel)
	require.Len(t, got, 3)

	// Order preserved (descending volume from GetTopSpecies).
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, "Strix aluco", got[2].ScientificName)

	// Totals are the FP-excluded hourly counts that drive the tooltip.
	assert.Equal(t, 10, got[0].Total)
	assert.Equal(t, 5, got[1].Total)
	assert.Equal(t, 1, got[2].Total)

	// Each species' buckets are a probability distribution (sum to 1.0).
	for i := range got {
		assert.InDelta(t, 1.0, bucketSum(&got[i].Buckets), 1e-9, "species %s buckets must sum to 1.0", got[i].ScientificName)
	}

	// Spot-check normalized values for the first species.
	assert.InDelta(t, 0.2, got[0].Buckets[0], 1e-9)
	assert.InDelta(t, 0.6, got[0].Buckets[12], 1e-9)
	assert.InDelta(t, 0.2, got[0].Buckets[18], 1e-9)
}

func TestBuildSpeciesHourlyDistribution_MergesLabelsSharingName(t *testing.T) {
	t.Parallel()

	// The same species detected under two model label IDs must collapse to one ridge whose
	// buckets are the summed counts across both labels (systemic multi-model behavior).
	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 60},
		{LabelID: 2, ScientificName: "Turdus merula", Count: 40},
		{LabelID: 3, ScientificName: "Erithacus rubecula", Count: 30},
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{6, 3}),  // Turdus merula via model A
		2: hours([2]int{18, 1}), // Turdus merula via model B
		3: hours([2]int{12, 2}), // Erithacus rubecula
	}

	got := buildSpeciesHourlyDistribution(top, hourlyByLabel)
	require.Len(t, got, 2) // two distinct species, not three label rows

	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, 4, got[0].Total) // 3 + 1 merged
	assert.InDelta(t, 0.75, got[0].Buckets[6], 1e-9)
	assert.InDelta(t, 0.25, got[0].Buckets[18], 1e-9)

	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, 2, got[1].Total)
}

func TestBuildSpeciesHourlyDistribution_DropsZeroTotalSpecies(t *testing.T) {
	t.Parallel()

	// A species ranked into the top-N by raw volume (GetTopSpecies does not exclude false
	// positives) but with no FP-excluded hourly detections must be dropped rather than rendered
	// as an empty ridge with "0 detections".
	top := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula", Count: 5},
		{LabelID: 2, ScientificName: "Pica pica", Count: 3}, // all false positives -> absent from hourly
	}
	hourlyByLabel := map[uint][24]int{
		1: hours([2]int{8, 5}),
	}

	got := buildSpeciesHourlyDistribution(top, hourlyByLabel)
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
}

func TestBuildSpeciesHourlyDistribution_Empty(t *testing.T) {
	t.Parallel()

	assert.Empty(t, buildSpeciesHourlyDistribution(nil, nil))

	// Non-empty top but no hourly data -> every species drops out, never nil.
	got := buildSpeciesHourlyDistribution(
		[]repository.SpeciesCount{{LabelID: 1, ScientificName: "Turdus merula", Count: 5}},
		map[uint][24]int{},
	)
	assert.Empty(t, got)
	assert.NotNil(t, got)
}

// epochAt returns the Unix epoch (seconds) for the given wall-clock time in loc.
func epochAt(loc *time.Location, year, month, day, hour, minute int) int64 {
	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, loc).Unix()
}

func TestHeatmapSlotResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		numDays int
		want    int
	}{
		{"single day stays fine", 1, heatmapSlotFine},
		{"month stays fine", 30, heatmapSlotFine},
		{"quarter boundary stays fine", heatmapMediumDays, heatmapSlotFine},
		{"just over quarter downsamples to medium", heatmapMediumDays + 1, heatmapSlotMedium},
		{"half year boundary stays medium", heatmapCoarseDays, heatmapSlotMedium},
		{"just over half year downsamples to coarse", heatmapCoarseDays + 1, heatmapSlotCoarse},
		{"full year is coarse", 365, heatmapSlotCoarse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, heatmapSlotResolution(tt.numDays))
		})
	}
}

func TestBuildActivityHeatmap_BucketingAndShape(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	timestamps := []int64{
		epochAt(utc, 2026, 3, 1, 0, 0),   // date 0, slot 0
		epochAt(utc, 2026, 3, 1, 0, 10),  // date 0, slot 0 (same cell -> count 2)
		epochAt(utc, 2026, 3, 1, 6, 30),  // date 0, slot 26
		epochAt(utc, 2026, 3, 2, 23, 59), // date 1, slot 95 (last 15-min slot)
		epochAt(utc, 2026, 3, 3, 12, 0),  // date 2, slot 48
	}

	got, err := buildActivityHeatmap(timestamps, utc, "2026-03-01", "2026-03-03")
	require.NoError(t, err)

	assert.Equal(t, []string{"2026-03-01", "2026-03-02", "2026-03-03"}, got.Dates)
	assert.Equal(t, heatmapSlotFine, got.SlotResolutionMinutes)

	// Cells are sparse (only non-zero), parallel, and sorted by (dateIndex, slot).
	require.Len(t, got.CellDateIndex, 4)
	require.Len(t, got.CellSlot, len(got.CellDateIndex))
	require.Len(t, got.CellCount, len(got.CellDateIndex))
	assert.Equal(t, []int{0, 0, 1, 2}, got.CellDateIndex)
	assert.Equal(t, []int{0, 26, 95, 48}, got.CellSlot)
	assert.Equal(t, []int{2, 1, 1, 1}, got.CellCount)
}

func TestBuildActivityHeatmap_TimezoneOffsetBuckets(t *testing.T) {
	t.Parallel()

	// Fixed +02:00 zone: a 23:30 UTC detection is 01:30 local the next day, and a
	// 22:30 UTC detection is 00:30 local two days on (outside a single-day window).
	loc := time.FixedZone("EET", 2*60*60)
	timestamps := []int64{
		time.Date(2026, 2, 28, 23, 30, 0, 0, time.UTC).Unix(), // local 2026-03-01 01:30 -> slot 6
		time.Date(2026, 3, 1, 22, 30, 0, 0, time.UTC).Unix(),  // local 2026-03-02 00:30 -> out of range
	}

	got, err := buildActivityHeatmap(timestamps, loc, "2026-03-01", "2026-03-01")
	require.NoError(t, err)

	assert.Equal(t, []string{"2026-03-01"}, got.Dates)
	assert.Equal(t, []int{0}, got.CellDateIndex)
	assert.Equal(t, []int{6}, got.CellSlot) // 90 minutes / 15 = slot 6
	assert.Equal(t, []int{1}, got.CellCount)
}

func TestBuildActivityHeatmap_DSTWallClockBuckets(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	// Both detections are at 13:00 wall-clock local time but on opposite sides of the
	// DST transition (EDT -04:00 in July, EST -05:00 in January). Per-timestamp
	// bucketing must place both at the same local slot regardless of the offset; a
	// single fixed-offset query would mis-bucket one of them.
	timestamps := []int64{
		epochAt(loc, 2026, 7, 1, 13, 0),  // summer, EDT
		epochAt(loc, 2026, 1, 15, 13, 0), // winter, EST
	}

	got, err := buildActivityHeatmap(timestamps, loc, "2026-01-01", "2026-12-31")
	require.NoError(t, err)
	// Full-year range downsamples to hourly slots.
	require.Equal(t, heatmapSlotCoarse, got.SlotResolutionMinutes)

	// Map each date index to its slot for clear assertions.
	slotByDate := map[string]int{}
	for i := range got.CellDateIndex {
		slotByDate[got.Dates[got.CellDateIndex[i]]] = got.CellSlot[i]
	}
	assert.Equal(t, 13, slotByDate["2026-07-01"], "summer 13:00 EDT should bucket to hour 13")
	assert.Equal(t, 13, slotByDate["2026-01-15"], "winter 13:00 EST should bucket to hour 13")
}

func TestBuildActivityHeatmap_DownsampleMedium(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	timestamps := []int64{
		epochAt(utc, 2026, 1, 1, 0, 45), // minute 45 -> slot 1 at 30-min resolution
		epochAt(utc, 2026, 1, 1, 1, 0),  // minute 60 -> slot 2 at 30-min resolution
	}

	// ~105 days exceeds the quarter threshold -> 30-minute slots.
	got, err := buildActivityHeatmap(timestamps, utc, "2026-01-01", "2026-04-15")
	require.NoError(t, err)
	assert.Equal(t, heatmapSlotMedium, got.SlotResolutionMinutes)
	assert.Equal(t, []int{1, 2}, got.CellSlot)
}

func TestBuildActivityHeatmap_OutOfRangeIgnored(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	timestamps := []int64{
		epochAt(utc, 2026, 2, 28, 12, 0), // before start
		epochAt(utc, 2026, 3, 2, 12, 0),  // in range
		epochAt(utc, 2026, 3, 5, 12, 0),  // after end
	}

	got, err := buildActivityHeatmap(timestamps, utc, "2026-03-01", "2026-03-03")
	require.NoError(t, err)
	assert.Equal(t, []int{1}, got.CellDateIndex) // only the in-range detection
	assert.Equal(t, []int{1}, got.CellCount)
}

func TestBuildActivityHeatmap_EmptyRangeHasDatesNoCells(t *testing.T) {
	t.Parallel()

	got, err := buildActivityHeatmap(nil, time.UTC, "2026-03-01", "2026-03-03")
	require.NoError(t, err)
	assert.Equal(t, []string{"2026-03-01", "2026-03-02", "2026-03-03"}, got.Dates)
	assert.Empty(t, got.CellDateIndex)
	assert.Empty(t, got.CellSlot)
	assert.Empty(t, got.CellCount)
}

func TestBuildActivityHeatmap_InvalidDates(t *testing.T) {
	t.Parallel()

	_, err := buildActivityHeatmap(nil, time.UTC, "not-a-date", "2026-03-03")
	require.Error(t, err)
}

// --- Dawn-chorus onset (buildDailyActivityOnset) ---------------------------

// dawnAt returns a civilDawnMinuteLookup that yields a constant civil dawn minute for every date.
func dawnAt(minute int) civilDawnMinuteLookup {
	return func(time.Time) (int, bool) { return minute, true }
}

// noCivilDawn is a civilDawnMinuteLookup that reports civil dawn undefined for every date (polar).
func noCivilDawn(time.Time) (int, bool) { return 0, false }

// onsetByDate maps the returned days by date string for clear, order-independent assertions.
func onsetByDate(days []datastore.DailyActivityOnset) map[string]datastore.DailyActivityOnset {
	m := make(map[string]datastore.DailyActivityOnset, len(days))
	for _, d := range days {
		m[d.Date] = d
	}
	return m
}

func TestBuildDailyActivityOnset_RankOnsetRelativeToCivilDawn(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	// Five detections at 05:00..05:40 (minutes 300..340). With rank 3 the onset is the 3rd
	// earliest = 05:20 (320). Civil dawn is fixed at 05:00 (300), so the relative onset is +20.
	timestamps := []int64{
		epochAt(utc, 2026, 3, 1, 5, 0),
		epochAt(utc, 2026, 3, 1, 5, 10),
		epochAt(utc, 2026, 3, 1, 5, 20),
		epochAt(utc, 2026, 3, 1, 5, 30),
		epochAt(utc, 2026, 3, 1, 5, 40),
	}

	days, err := buildDailyActivityOnset(timestamps, utc, "2026-03-01", "2026-03-01", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, "2026-03-01", days[0].Date)
	assert.Equal(t, 5, days[0].DetectionCount)
	require.NotNil(t, days[0].OnsetRelMinutes)
	assert.Equal(t, 20, *days[0].OnsetRelMinutes)
}

func TestBuildDailyActivityOnset_TooFewDetectionsNullButCounted(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	// Four detections is below the min of 5: the day is emitted with its count but a nil onset.
	timestamps := []int64{
		epochAt(utc, 2026, 3, 1, 5, 0),
		epochAt(utc, 2026, 3, 1, 5, 10),
		epochAt(utc, 2026, 3, 1, 5, 20),
		epochAt(utc, 2026, 3, 1, 5, 30),
	}

	days, err := buildDailyActivityOnset(timestamps, utc, "2026-03-01", "2026-03-01", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, 4, days[0].DetectionCount)
	assert.Nil(t, days[0].OnsetRelMinutes, "below min-count days keep their count but have no onset")
}

func TestBuildDailyActivityOnset_PolarDayNull(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	timestamps := []int64{
		epochAt(utc, 2026, 6, 21, 2, 0),
		epochAt(utc, 2026, 6, 21, 2, 10),
		epochAt(utc, 2026, 6, 21, 2, 20),
		epochAt(utc, 2026, 6, 21, 2, 30),
		epochAt(utc, 2026, 6, 21, 2, 40),
	}

	// Civil dawn undefined (polar day): plenty of detections, but the onset is still nil.
	days, err := buildDailyActivityOnset(timestamps, utc, "2026-06-21", "2026-06-21", onsetDetectionRank, minOnsetDetections, noCivilDawn)
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, 5, days[0].DetectionCount)
	assert.Nil(t, days[0].OnsetRelMinutes)
}

func TestBuildDailyActivityOnset_ImmuneToLaterInDayVolume(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	// Both days share the same five morning detections; day 2 adds a large afternoon burst.
	// The rank-3 onset (the 3rd earliest detection) must be identical, proving the metric is not
	// pulled later by unrelated afternoon volume the way a daily percentile would be.
	timestamps := []int64{
		// 2026-03-01: morning only.
		epochAt(utc, 2026, 3, 1, 5, 0),
		epochAt(utc, 2026, 3, 1, 5, 10),
		epochAt(utc, 2026, 3, 1, 5, 20),
		epochAt(utc, 2026, 3, 1, 5, 30),
		epochAt(utc, 2026, 3, 1, 5, 40),
		// 2026-03-02: same morning detections...
		epochAt(utc, 2026, 3, 2, 5, 0),
		epochAt(utc, 2026, 3, 2, 5, 10),
		epochAt(utc, 2026, 3, 2, 5, 20),
		epochAt(utc, 2026, 3, 2, 5, 30),
		epochAt(utc, 2026, 3, 2, 5, 40),
		// ...plus a big afternoon burst that must not move the onset.
		epochAt(utc, 2026, 3, 2, 14, 0),
		epochAt(utc, 2026, 3, 2, 14, 5),
		epochAt(utc, 2026, 3, 2, 14, 10),
		epochAt(utc, 2026, 3, 2, 14, 15),
		epochAt(utc, 2026, 3, 2, 14, 20),
		epochAt(utc, 2026, 3, 2, 14, 25),
	}

	days, err := buildDailyActivityOnset(timestamps, utc, "2026-03-01", "2026-03-02", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	byDate := onsetByDate(days)

	require.NotNil(t, byDate["2026-03-01"].OnsetRelMinutes)
	require.NotNil(t, byDate["2026-03-02"].OnsetRelMinutes)
	assert.Equal(t, 20, *byDate["2026-03-01"].OnsetRelMinutes)
	assert.Equal(t, *byDate["2026-03-01"].OnsetRelMinutes, *byDate["2026-03-02"].OnsetRelMinutes,
		"onset must not shift when later-in-day volume grows")
	assert.Equal(t, 5, byDate["2026-03-01"].DetectionCount)
	assert.Equal(t, 11, byDate["2026-03-02"].DetectionCount)
}

func TestBuildDailyActivityOnset_FullDateAxisWithGaps(t *testing.T) {
	t.Parallel()

	// No detections: every date in the inclusive range is still emitted (count 0, nil onset) so
	// the client has a continuous date axis and its trend line can break over the gaps.
	days, err := buildDailyActivityOnset(nil, time.UTC, "2026-03-01", "2026-03-03", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	require.Len(t, days, 3)
	assert.Equal(t, []string{"2026-03-01", "2026-03-02", "2026-03-03"},
		[]string{days[0].Date, days[1].Date, days[2].Date})
	for _, d := range days {
		assert.Zero(t, d.DetectionCount)
		assert.Nil(t, d.OnsetRelMinutes)
	}
}

func TestBuildDailyActivityOnset_TimezoneBucketing(t *testing.T) {
	t.Parallel()

	// Fixed +02:00 zone: detections at 03:00..03:40 UTC are 05:00..05:40 local on 2026-03-01, so
	// they bucket onto the local day and the onset is computed in local minutes (rank-3 = 05:20).
	loc := time.FixedZone("EET", 2*60*60)
	base := time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC)
	timestamps := []int64{
		base.Unix(),
		base.Add(10 * time.Minute).Unix(),
		base.Add(20 * time.Minute).Unix(),
		base.Add(30 * time.Minute).Unix(),
		base.Add(40 * time.Minute).Unix(),
	}

	days, err := buildDailyActivityOnset(timestamps, loc, "2026-03-01", "2026-03-01", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Equal(t, 5, days[0].DetectionCount)
	require.NotNil(t, days[0].OnsetRelMinutes)
	assert.Equal(t, 20, *days[0].OnsetRelMinutes) // local onset 05:20 minus civil dawn 05:00
}

func TestBuildDailyActivityOnset_OutOfRangeDetectionsIgnored(t *testing.T) {
	t.Parallel()

	utc := time.UTC
	// Five detections on 2026-02-28, outside the requested [2026-03-01, 2026-03-01] range.
	timestamps := []int64{
		epochAt(utc, 2026, 2, 28, 5, 0),
		epochAt(utc, 2026, 2, 28, 5, 10),
		epochAt(utc, 2026, 2, 28, 5, 20),
		epochAt(utc, 2026, 2, 28, 5, 30),
		epochAt(utc, 2026, 2, 28, 5, 40),
	}

	days, err := buildDailyActivityOnset(timestamps, utc, "2026-03-01", "2026-03-01", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.NoError(t, err)
	require.Len(t, days, 1)
	assert.Zero(t, days[0].DetectionCount, "detections outside the range are not counted")
	assert.Nil(t, days[0].OnsetRelMinutes)
}

func TestBuildDailyActivityOnset_InvalidDates(t *testing.T) {
	t.Parallel()

	_, err := buildDailyActivityOnset(nil, time.UTC, "not-a-date", "2026-03-03", onsetDetectionRank, minOnsetDetections, dawnAt(300))
	require.Error(t, err)
}
