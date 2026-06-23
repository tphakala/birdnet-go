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

// --- Confidence distribution (buildSpeciesConfidenceHistogram) -------------
//
// These exercise the pure binning/normalization. False-positive exclusion happens upstream in the
// repository query (GetBatchConfidences), so the confByLabel inputs here are already FP-excluded.

// confidenceBinSum sums a species' normalized bins (should be ~1.0 for any non-empty species).
func confidenceBinSum(bins []float64) float64 {
	sum := 0.0
	for _, v := range bins {
		sum += v
	}
	return sum
}

func TestConfidenceBinIndex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		conf float64
		bins int
		want int
	}{
		{"zero lands in the first bin", 0.0, 20, 0},
		{"mid lands in the middle bin", 0.5, 20, 10},
		{"just under one lands in the last bin", 0.999, 20, 19},
		{"exactly one is clamped into the last bin", 1.0, 20, 19},
		{"above one is clamped into the last bin", 1.5, 20, 19},
		{"negative is clamped into the first bin", -0.2, 20, 0},
		{"five-bin boundary", 0.4, 5, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, confidenceBinIndex(tt.conf, tt.bins))
		})
	}
}

func TestBuildSpeciesConfidenceHistogram_BinsAndNormalizes(t *testing.T) {
	t.Parallel()

	// Four detections across four equal bins (width 0.25): 0.1->bin0, 0.3->bin1, 0.55->bin2,
	// 0.9->bin3. Each is 1/4 of the total, so every normalized bin is 0.25.
	species := []repository.SpeciesCount{{LabelID: 1, ScientificName: "Turdus merula"}}
	confByLabel := map[uint][]float64{1: {0.1, 0.3, 0.55, 0.9}}

	got := buildSpeciesConfidenceHistogram(species, confByLabel, 4, 1)
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, 4, got[0].Total)
	require.Len(t, got[0].Bins, 4)
	for i, want := range []float64{0.25, 0.25, 0.25, 0.25} {
		assert.InDelta(t, want, got[0].Bins[i], 1e-9)
	}
	assert.InDelta(t, 1.0, confidenceBinSum(got[0].Bins), 1e-9, "normalized bins sum to 1.0")
}

func TestBuildSpeciesConfidenceHistogram_BinBoundaries(t *testing.T) {
	t.Parallel()

	// Confidence exactly 0.0 must land in the first bin and exactly 1.0 in the last (clamped, not
	// overflowing the slice).
	species := []repository.SpeciesCount{{LabelID: 1, ScientificName: "Strix aluco"}}
	confByLabel := map[uint][]float64{1: {0.0, 1.0}}

	got := buildSpeciesConfidenceHistogram(species, confByLabel, 5, 1)
	require.Len(t, got, 1)
	require.Len(t, got[0].Bins, 5)
	assert.Equal(t, 2, got[0].Total)
	assert.InDelta(t, 0.5, got[0].Bins[0], 1e-9) // 0.0 -> bin 0
	assert.InDelta(t, 0.5, got[0].Bins[4], 1e-9) // 1.0 -> last bin
}

func TestBuildSpeciesConfidenceHistogram_BinCountParam(t *testing.T) {
	t.Parallel()

	species := []repository.SpeciesCount{{LabelID: 1, ScientificName: "Turdus merula"}}
	confByLabel := map[uint][]float64{1: {0.2, 0.4, 0.6, 0.8}}

	for _, bins := range []int{10, 20, 50} {
		got := buildSpeciesConfidenceHistogram(species, confByLabel, bins, 1)
		require.Len(t, got, 1)
		assert.Len(t, got[0].Bins, bins, "bin count param is honored")
		assert.InDelta(t, 1.0, confidenceBinSum(got[0].Bins), 1e-9)
	}

	// Non-positive bins yields an empty (non-nil) result rather than panicking.
	empty := buildSpeciesConfidenceHistogram(species, confByLabel, 0, 1)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)
}

func TestBuildSpeciesConfidenceHistogram_MinCountFilter(t *testing.T) {
	t.Parallel()

	// Pica pica has only 2 detections, below the floor of 5, so it is dropped as noisy.
	species := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula"},
		{LabelID: 2, ScientificName: "Pica pica"},
	}
	confByLabel := map[uint][]float64{
		1: {0.5, 0.6, 0.7, 0.8, 0.9},
		2: {0.5, 0.6},
	}

	got := buildSpeciesConfidenceHistogram(species, confByLabel, 10, 5)
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
}

func TestBuildSpeciesConfidenceHistogram_MergesLabelsSharingName(t *testing.T) {
	t.Parallel()

	// The same species detected under two model label IDs collapses to one row whose confidences are
	// the concatenation across both labels (systemic multi-model behavior), preserving order.
	species := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula"},
		{LabelID: 2, ScientificName: "Turdus merula"},
		{LabelID: 3, ScientificName: "Erithacus rubecula"},
	}
	confByLabel := map[uint][]float64{
		1: {0.1, 0.2}, // Turdus merula via model A -> bin 0 (width 0.25)
		2: {0.8, 0.9}, // Turdus merula via model B -> bin 3
		3: {0.5, 0.5, 0.5},
	}

	got := buildSpeciesConfidenceHistogram(species, confByLabel, 4, 1)
	require.Len(t, got, 2) // two distinct species, not three label rows

	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, 4, got[0].Total) // 2 + 2 merged
	assert.InDelta(t, 0.5, got[0].Bins[0], 1e-9)
	assert.InDelta(t, 0.5, got[0].Bins[3], 1e-9)

	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, 3, got[1].Total)
}

func TestBuildSpeciesConfidenceHistogram_PreservesVolumeOrder(t *testing.T) {
	t.Parallel()

	species := []repository.SpeciesCount{
		{LabelID: 1, ScientificName: "Turdus merula"},
		{LabelID: 2, ScientificName: "Erithacus rubecula"},
		{LabelID: 3, ScientificName: "Strix aluco"},
	}
	confByLabel := map[uint][]float64{
		1: {0.5, 0.6, 0.7},
		2: {0.4, 0.5, 0.6},
		3: {0.7, 0.8, 0.9},
	}

	got := buildSpeciesConfidenceHistogram(species, confByLabel, 10, 1)
	require.Len(t, got, 3)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "Erithacus rubecula", got[1].ScientificName)
	assert.Equal(t, "Strix aluco", got[2].ScientificName)
}

func TestBuildSpeciesConfidenceHistogram_Empty(t *testing.T) {
	t.Parallel()

	empty := buildSpeciesConfidenceHistogram(nil, nil, 20, 1)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)

	// A species ranked in by raw volume but with no FP-excluded confidences drops out, never nil.
	got := buildSpeciesConfidenceHistogram(
		[]repository.SpeciesCount{{LabelID: 1, ScientificName: "Turdus merula"}},
		map[uint][]float64{},
		20, 1,
	)
	assert.Empty(t, got)
	assert.NotNil(t, got)
}

// accumTestZone is a fixed UTC+2 zone for deterministic accumulation date-bucketing tests (no DST
// jumps), so a timestamp's local calendar date is unambiguous.
var accumTestZone = time.FixedZone("UTC+2", 2*60*60)

// localUnix returns the Unix epoch (seconds) for the given wall-clock time in loc, for terse first-seen
// fixtures.
func localUnix(loc *time.Location, year int, month time.Month, day, hour, minute int) int64 {
	return time.Date(year, month, day, hour, minute, 0, 0, loc).Unix()
}

func TestBuildSpeciesAccumulation_Empty(t *testing.T) {
	t.Parallel()

	// Nil input over a 3-day range: one zero point per day, never nil.
	got, err := buildSpeciesAccumulation(nil, accumTestZone, "2026-06-01", "2026-06-03")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got, 3)
	for i := range got {
		assert.Equal(t, 0, got[i].CumulativeSpecies)
		assert.Equal(t, 0, got[i].NewSpecies)
	}
	assert.Equal(t, "2026-06-01", got[0].Date)
	assert.Equal(t, "2026-06-03", got[2].Date)
}

func TestBuildSpeciesAccumulation_CumulativeMonotonic(t *testing.T) {
	t.Parallel()

	// Three species first seen on day 1, day 2, day 4 of a 5-day window; day 3 and day 5 add none.
	firstSeen := []repository.SpeciesFirstSeen{
		{ScientificName: "Turdus merula", FirstDetected: localUnix(accumTestZone, 2026, 6, 1, 5, 30)},
		{ScientificName: "Erithacus rubecula", FirstDetected: localUnix(accumTestZone, 2026, 6, 2, 6, 0)},
		{ScientificName: "Strix aluco", FirstDetected: localUnix(accumTestZone, 2026, 6, 4, 23, 0)},
	}

	got, err := buildSpeciesAccumulation(firstSeen, accumTestZone, "2026-06-01", "2026-06-05")
	require.NoError(t, err)
	require.Len(t, got, 5)

	wantCumulative := []int{1, 2, 2, 3, 3}
	wantNew := []int{1, 1, 0, 1, 0}
	prev := 0
	for i := range got {
		assert.Equalf(t, wantCumulative[i], got[i].CumulativeSpecies, "cumulative on day %d", i+1)
		assert.Equalf(t, wantNew[i], got[i].NewSpecies, "new on day %d", i+1)
		assert.GreaterOrEqualf(t, got[i].CumulativeSpecies, prev, "curve must be monotonic non-decreasing")
		prev = got[i].CumulativeSpecies
	}
	// The final cumulative equals the number of distinct in-period species.
	assert.Equal(t, 3, got[len(got)-1].CumulativeSpecies)
}

func TestBuildSpeciesAccumulation_SameDayFirsts(t *testing.T) {
	t.Parallel()

	// Two species first seen on the same day jump the cumulative by two on that day.
	firstSeen := []repository.SpeciesFirstSeen{
		{ScientificName: "Turdus merula", FirstDetected: localUnix(accumTestZone, 2026, 6, 2, 4, 0)},
		{ScientificName: "Erithacus rubecula", FirstDetected: localUnix(accumTestZone, 2026, 6, 2, 7, 0)},
	}

	got, err := buildSpeciesAccumulation(firstSeen, accumTestZone, "2026-06-01", "2026-06-02")
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, 0, got[0].CumulativeSpecies)
	assert.Equal(t, 0, got[0].NewSpecies)
	assert.Equal(t, 2, got[1].CumulativeSpecies)
	assert.Equal(t, 2, got[1].NewSpecies)
}

func TestBuildSpeciesAccumulation_OutOfRangeIgnored(t *testing.T) {
	t.Parallel()

	// First-seen dates outside the enumerated range must not be counted (defensive: SQL already
	// bounds them, but a row skewed by loc onto an out-of-range day must never inflate the curve).
	firstSeen := []repository.SpeciesFirstSeen{
		{ScientificName: "Before range", FirstDetected: localUnix(accumTestZone, 2026, 5, 30, 12, 0)},
		{ScientificName: "In range", FirstDetected: localUnix(accumTestZone, 2026, 6, 2, 12, 0)},
		{ScientificName: "After range", FirstDetected: localUnix(accumTestZone, 2026, 6, 10, 12, 0)},
	}

	got, err := buildSpeciesAccumulation(firstSeen, accumTestZone, "2026-06-01", "2026-06-03")
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Only the single in-range species accumulates.
	assert.Equal(t, 1, got[len(got)-1].CumulativeSpecies)
	assert.Equal(t, 1, got[1].NewSpecies)
}

func TestBuildSpeciesAccumulation_TimezoneDateAssignment(t *testing.T) {
	t.Parallel()

	// A 23:30 local detection belongs to its local date, not the next UTC day. In UTC+2 this epoch
	// is 21:30 UTC the same date; bucketing in loc keeps it on 2026-06-01.
	firstSeen := []repository.SpeciesFirstSeen{
		{ScientificName: "Late singer", FirstDetected: localUnix(accumTestZone, 2026, 6, 1, 23, 30)},
	}

	got, err := buildSpeciesAccumulation(firstSeen, accumTestZone, "2026-06-01", "2026-06-02")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, got[0].NewSpecies, "23:30 local must land on its own local date")
	assert.Equal(t, 1, got[0].CumulativeSpecies)
	assert.Equal(t, 0, got[1].NewSpecies)
}

func TestBuildSpeciesAccumulation_MidnightDSTEnumeration(t *testing.T) {
	t.Parallel()

	// A timezone whose spring-forward DST transition skips midnight (clocks jump 23:59:59 -> 01:00:00)
	// would drift a loc-based day enumeration and drop the final day. The date axis must still be the
	// exact inclusive calendar range. Cuba sprang forward at 00:00 local on 2018-03-11. Skip when the
	// zone is unavailable (minimal images without tzdata), like the heatmap DST test.
	loc, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	got, err := buildSpeciesAccumulation(nil, loc, "2018-03-09", "2018-03-13")
	require.NoError(t, err)

	wantDates := []string{"2018-03-09", "2018-03-10", "2018-03-11", "2018-03-12", "2018-03-13"}
	require.Len(t, got, len(wantDates), "every calendar day in the range must be emitted across the DST jump")
	for i, want := range wantDates {
		assert.Equalf(t, want, got[i].Date, "day index %d", i)
		assert.Equal(t, 0, got[i].CumulativeSpecies)
	}
}

func TestBuildSpeciesAccumulation_NilLocDefaultsUTC(t *testing.T) {
	t.Parallel()

	firstSeen := []repository.SpeciesFirstSeen{
		{ScientificName: "Turdus merula", FirstDetected: localUnix(time.UTC, 2026, 6, 1, 12, 0)},
	}
	got, err := buildSpeciesAccumulation(firstSeen, nil, "2026-06-01", "2026-06-01")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].CumulativeSpecies)
}

func TestBuildSpeciesAccumulation_InvalidDate(t *testing.T) {
	t.Parallel()

	_, err := buildSpeciesAccumulation(nil, accumTestZone, "not-a-date", "2026-06-03")
	require.Error(t, err)

	_, err = buildSpeciesAccumulation(nil, accumTestZone, "2026-06-01", "bad")
	require.Error(t, err)
}

// ---- Year-over-year tracker (#1197) ----

func TestBuildYearOverYear_Empty(t *testing.T) {
	t.Parallel()

	// Nil input over a 3-day window: one zero point per current-year day, never nil, with year labels.
	got, err := buildYearOverYear(nil, nil, accumTestZone,
		"2026-01-01", "2026-01-03", "2025-01-01", "2025-01-03", 2026, 2025)
	require.NoError(t, err)
	require.NotNil(t, got.Points)
	require.Len(t, got.Points, 3)
	assert.Equal(t, 2026, got.CurrentYear)
	assert.Equal(t, 2025, got.PreviousYear)
	for i := range got.Points {
		assert.Equal(t, 0, got.Points[i].ThisYear)
		assert.Equal(t, 0, got.Points[i].LastYear)
		assert.Equal(t, 0, got.Points[i].Delta)
	}
	assert.Equal(t, "2026-01-01", got.Points[0].Date)
	assert.Equal(t, "01-01", got.Points[0].MonthDay)
	assert.Equal(t, "2026-01-03", got.Points[2].Date)
}

func TestBuildYearOverYear_CumulativeAndDelta(t *testing.T) {
	t.Parallel()

	// This year ahead: 2 detections on Jan 1, 1 on Jan 3. Last year: 1 on Jan 2.
	thisTs := []int64{
		localUnix(accumTestZone, 2026, 1, 1, 6, 0),
		localUnix(accumTestZone, 2026, 1, 1, 7, 0),
		localUnix(accumTestZone, 2026, 1, 3, 8, 0),
	}
	lastTs := []int64{
		localUnix(accumTestZone, 2025, 1, 2, 6, 0),
	}

	got, err := buildYearOverYear(thisTs, lastTs, accumTestZone,
		"2026-01-01", "2026-01-03", "2025-01-01", "2025-01-03", 2026, 2025)
	require.NoError(t, err)
	require.Len(t, got.Points, 3)

	wantThis := []int{2, 2, 3}
	wantLast := []int{0, 1, 1}
	prevThis, prevLast := 0, 0
	for i := range got.Points {
		assert.Equalf(t, wantThis[i], got.Points[i].ThisYear, "thisYear on day %d", i)
		assert.Equalf(t, wantLast[i], got.Points[i].LastYear, "lastYear on day %d", i)
		assert.Equalf(t, wantThis[i]-wantLast[i], got.Points[i].Delta, "delta on day %d", i)
		assert.GreaterOrEqual(t, got.Points[i].ThisYear, prevThis, "thisYear must be monotonic")
		assert.GreaterOrEqual(t, got.Points[i].LastYear, prevLast, "lastYear must be monotonic")
		prevThis, prevLast = got.Points[i].ThisYear, got.Points[i].LastYear
	}
}

func TestBuildYearOverYear_CurrentLeapPriorNonLeap_Feb29CarryForward(t *testing.T) {
	t.Parallel()

	// Current year 2024 is a leap year; previous year 2023 is not. The previous cumulative must carry
	// its Feb 28 value flat across the current Feb 29 (no prior counterpart), and stay monotonic.
	thisTs := []int64{
		localUnix(accumTestZone, 2024, 2, 28, 6, 0),
		localUnix(accumTestZone, 2024, 2, 29, 6, 0),
	}
	lastTs := []int64{
		localUnix(accumTestZone, 2023, 2, 28, 6, 0),
	}

	got, err := buildYearOverYear(thisTs, lastTs, accumTestZone,
		"2024-02-27", "2024-03-01", "2023-02-27", "2023-03-01", 2024, 2023)
	require.NoError(t, err)
	require.Len(t, got.Points, 4) // Feb 27, 28, 29, Mar 1

	// Index 2 is Feb 29 (only exists in the leap current year).
	assert.Equal(t, "2024-02-29", got.Points[2].Date)
	assert.Equal(t, "02-29", got.Points[2].MonthDay)
	// thisYear climbs 1 -> 2 across Feb 28 -> Feb 29; lastYear holds flat at 1 (carry-forward).
	assert.Equal(t, 2, got.Points[2].ThisYear)
	assert.Equal(t, got.Points[1].LastYear, got.Points[2].LastYear, "lastYear carries flat across Feb 29")
	assert.Equal(t, 1, got.Points[2].LastYear)
	assert.Equal(t, 1, got.Points[2].Delta)
}

func TestBuildYearOverYear_PriorLeapCurrentNonLeap_Feb29FoldIn(t *testing.T) {
	t.Parallel()

	// Previous year 2024 is a leap year with a Feb 29 detection; current year 2025 is not. The current
	// axis steps Feb 28 -> Mar 1, so the prior Feb 29 must fold into the Mar 1 previous cumulative; no
	// prior detection may be lost.
	thisTs := []int64{
		localUnix(accumTestZone, 2025, 3, 1, 6, 0),
	}
	lastTs := []int64{
		localUnix(accumTestZone, 2024, 2, 28, 6, 0),
		localUnix(accumTestZone, 2024, 2, 29, 6, 0),
		localUnix(accumTestZone, 2024, 3, 1, 6, 0),
	}

	got, err := buildYearOverYear(thisTs, lastTs, accumTestZone,
		"2025-02-27", "2025-03-01", "2024-02-27", "2024-03-01", 2025, 2024)
	require.NoError(t, err)
	require.Len(t, got.Points, 3) // Feb 27, 28, Mar 1 (no Feb 29 in the non-leap current year)

	last := got.Points[len(got.Points)-1]
	assert.Equal(t, "2025-03-01", last.Date)
	// lastYear jumps from 1 (after Feb 28) to 3 at Mar 1: +2 folds in BOTH the prior Feb 29 and Mar 1.
	assert.Equal(t, 1, got.Points[1].LastYear)
	assert.Equal(t, 3, last.LastYear, "prior Feb 29 must fold into Mar 1; totals preserved")
	assert.Equal(t, 1, last.ThisYear)
	assert.Equal(t, -2, last.Delta)
}

func TestBuildYearOverYear_PartialYearTotals(t *testing.T) {
	t.Parallel()

	// A mid-year end date emits one point per day from Jan 1 and the final cumulatives equal the totals.
	thisTs := []int64{
		localUnix(accumTestZone, 2026, 1, 10, 6, 0),
		localUnix(accumTestZone, 2026, 3, 15, 6, 0),
		localUnix(accumTestZone, 2026, 6, 23, 6, 0),
	}
	lastTs := []int64{
		localUnix(accumTestZone, 2025, 2, 1, 6, 0),
		localUnix(accumTestZone, 2025, 5, 5, 6, 0),
	}

	got, err := buildYearOverYear(thisTs, lastTs, accumTestZone,
		"2026-01-01", "2026-06-23", "2025-01-01", "2025-06-23", 2026, 2025)
	require.NoError(t, err)
	require.Len(t, got.Points, 174) // Jan 1 .. Jun 23 inclusive in a non-leap year

	last := got.Points[len(got.Points)-1]
	assert.Equal(t, "2026-06-23", last.Date)
	assert.Equal(t, 3, last.ThisYear, "final thisYear cumulative equals this-year detection total")
	assert.Equal(t, 2, last.LastYear, "final lastYear cumulative equals last-year detection total")
	assert.Equal(t, 1, last.Delta)
}

func TestBuildYearOverYear_EmptyThisYear(t *testing.T) {
	t.Parallel()

	lastTs := []int64{localUnix(accumTestZone, 2025, 1, 2, 6, 0)}
	got, err := buildYearOverYear(nil, lastTs, accumTestZone,
		"2026-01-01", "2026-01-03", "2025-01-01", "2025-01-03", 2026, 2025)
	require.NoError(t, err)
	require.Len(t, got.Points, 3)

	last := got.Points[len(got.Points)-1]
	assert.Equal(t, 0, last.ThisYear)
	assert.Equal(t, 1, last.LastYear)
	assert.Equal(t, -1, last.Delta, "behind last year reads as a negative delta")
}

func TestBuildYearOverYear_EmptyLastYear(t *testing.T) {
	t.Parallel()

	thisTs := []int64{localUnix(accumTestZone, 2026, 1, 2, 6, 0)}
	got, err := buildYearOverYear(thisTs, nil, accumTestZone,
		"2026-01-01", "2026-01-03", "2025-01-01", "2025-01-03", 2026, 2025)
	require.NoError(t, err)
	require.Len(t, got.Points, 3)

	last := got.Points[len(got.Points)-1]
	assert.Equal(t, 1, last.ThisYear)
	assert.Equal(t, 0, last.LastYear)
	assert.Equal(t, 1, last.Delta)
}

func TestBuildYearOverYear_MidnightDSTEnumeration(t *testing.T) {
	t.Parallel()

	// A timezone whose spring-forward DST transition skips midnight must not drift the date-axis
	// enumeration. Cuba sprang forward at 00:00 local on 2018-03-11. Skip when tzdata is unavailable.
	loc, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}

	got, err := buildYearOverYear(nil, nil, loc,
		"2018-03-09", "2018-03-13", "2017-03-09", "2017-03-13", 2018, 2017)
	require.NoError(t, err)

	wantDates := []string{"2018-03-09", "2018-03-10", "2018-03-11", "2018-03-12", "2018-03-13"}
	require.Len(t, got.Points, len(wantDates), "every calendar day must be emitted across the DST jump")
	for i, want := range wantDates {
		assert.Equalf(t, want, got.Points[i].Date, "day index %d", i)
	}
}

func TestBuildYearOverYear_NilLocDefaultsUTC(t *testing.T) {
	t.Parallel()

	thisTs := []int64{localUnix(time.UTC, 2026, 1, 1, 12, 0)}
	got, err := buildYearOverYear(thisTs, nil, nil,
		"2026-01-01", "2026-01-01", "2025-01-01", "2025-01-01", 2026, 2025)
	require.NoError(t, err)
	require.Len(t, got.Points, 1)
	assert.Equal(t, 1, got.Points[0].ThisYear)
}

func TestBuildYearOverYear_InvalidDate(t *testing.T) {
	t.Parallel()

	_, err := buildYearOverYear(nil, nil, accumTestZone,
		"bad", "2026-01-03", "2025-01-01", "2025-01-03", 2026, 2025)
	require.Error(t, err)

	_, err = buildYearOverYear(nil, nil, accumTestZone,
		"2026-01-01", "2026-01-03", "2025-01-01", "nope", 2026, 2025)
	require.Error(t, err)
}

func TestComputeYearOverYearWindows_Feb29PriorNonLeapClamps(t *testing.T) {
	t.Parallel()

	// Requesting Feb 29 of a leap year must clamp the previous window end to Feb 28 (the previous year
	// is not a leap year), and the exclusive epoch end must be Mar 1 of the previous year - never Mar 2
	// (which would happen if Feb 29 silently rolled forward before the +1-day step).
	ref := time.Date(2024, time.February, 29, 12, 0, 0, 0, time.UTC)
	w := computeYearOverYearWindows(ref, time.UTC)

	assert.Equal(t, 2024, w.curYear)
	assert.Equal(t, 2023, w.prevYear)
	assert.Equal(t, "2024-01-01", w.curStart)
	assert.Equal(t, "2024-02-29", w.curEnd)
	assert.Equal(t, "2023-01-01", w.priorStart)
	assert.Equal(t, "2023-02-28", w.priorEnd)

	wantPriorEnd := time.Date(2023, time.March, 1, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, wantPriorEnd, w.priorEndEpoch, "exclusive prior end is Mar 1, not Mar 2")
	wantCurEnd := time.Date(2024, time.March, 1, 0, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, wantCurEnd, w.curEndEpoch)
}

func TestComputeYearOverYearWindows_Jan1Boundary(t *testing.T) {
	t.Parallel()

	// On Jan 1 both windows collapse to a single day, and the chart emits exactly one point. This guards
	// the priorEnd +1-day boundary against an off-by-one that would query zero days.
	ref := time.Date(2026, time.January, 1, 8, 0, 0, 0, time.UTC)
	w := computeYearOverYearWindows(ref, time.UTC)

	assert.Equal(t, "2026-01-01", w.curStart)
	assert.Equal(t, "2026-01-01", w.curEnd)
	assert.Equal(t, "2025-01-01", w.priorStart)
	assert.Equal(t, "2025-01-01", w.priorEnd)

	got, err := buildYearOverYear(nil, nil, time.UTC,
		w.curStart, w.curEnd, w.priorStart, w.priorEnd, w.curYear, w.prevYear)
	require.NoError(t, err)
	require.Len(t, got.Points, 1)
	assert.Equal(t, "2026-01-01", got.Points[0].Date)
	assert.Equal(t, "01-01", got.Points[0].MonthDay)
}

func TestComputeYearOverYearWindows_MidYearNoClamp(t *testing.T) {
	t.Parallel()

	ref := time.Date(2026, time.June, 23, 0, 0, 0, 0, time.UTC)
	w := computeYearOverYearWindows(ref, time.UTC)

	assert.Equal(t, "2026-06-23", w.curEnd)
	assert.Equal(t, "2025-06-23", w.priorEnd, "non-leap-edge dates map straight across years")
}

func TestComputeYearOverYearWindows_ProjectsRefIntoLoc(t *testing.T) {
	t.Parallel()

	// A ref whose own zone reads Jan 1 2024 but which is Dec 31 2023 in loc must resolve to loc's
	// calendar date, not ref's. 2024-01-01 06:00 at UTC+12 is 2023-12-31 18:00 UTC.
	east := time.FixedZone("UTC+12", 12*60*60)
	ref := time.Date(2024, time.January, 1, 6, 0, 0, 0, east)
	w := computeYearOverYearWindows(ref, time.UTC)

	assert.Equal(t, 2023, w.curYear, "calendar date derived in loc, not ref's zone")
	assert.Equal(t, "2023-12-31", w.curEnd)
	assert.Equal(t, 2022, w.prevYear)
	assert.Equal(t, "2022-12-31", w.priorEnd)
}
