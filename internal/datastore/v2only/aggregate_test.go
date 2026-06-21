package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
