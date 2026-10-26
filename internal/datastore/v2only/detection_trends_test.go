package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const trendsSpecies = "Fringilla coelebs"

const trendReadingLayout = "2006-01-02 15:04"

// trendInstant returns the instant of a local wall reading "YYYY-MM-DD HH:MM" in loc (the first
// occurrence in a repeated hour; no case seeds one).
func trendInstant(t *testing.T, loc *time.Location, reading string) time.Time {
	t.Helper()
	wall, err := time.Parse(trendReadingLayout, reading)
	require.NoError(t, err)
	return wallclock.At(wallclock.DateOf(wall), wall.Hour(), wall.Minute(), 0, loc)
}

func TestV2OnlyDatastore_DetectionTrendsBucketByLocalDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		zone     string
		period   string
		now      string // local "YYYY-MM-DD HH:MM"
		readings []string
		want     []datastore.DailyAnalyticsData // newest first
	}{
		{
			// The reported case: 00:30 on 2026-10-24 is +03:00, before the fall back on 10-25, and
			// must stay on 10-24. The row 30 minutes before the first date is outside the window.
			name:   "Helsinki week across the fall back",
			zone:   "Europe/Helsinki",
			period: "week",
			now:    "2026-10-27 12:00",
			readings: []string{
				"2026-10-20 23:30", // before the first date (10-21)
				"2026-10-21 00:30", "2026-10-23 23:30", "2026-10-24 00:30",
				"2026-10-24 23:30", "2026-10-25 00:30", "2026-10-25 23:30", "2026-10-26 00:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-10-26", Count: 1},
				{Date: "2026-10-25", Count: 2},
				{Date: "2026-10-24", Count: 2},
				{Date: "2026-10-23", Count: 1},
				{Date: "2026-10-21", Count: 1},
			},
		},
		{
			name:   "New York week across the fall back",
			zone:   "America/New_York",
			period: "week",
			now:    "2026-11-03 12:00",
			readings: []string{
				"2026-10-27 23:30", // before the first date (10-28)
				"2026-10-28 00:30", "2026-10-31 23:30", "2026-11-01 00:30",
				"2026-11-01 23:30", "2026-11-02 00:30", "2026-11-02 23:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-11-02", Count: 2},
				{Date: "2026-11-01", Count: 2},
				{Date: "2026-10-31", Count: 1},
				{Date: "2026-10-28", Count: 1},
			},
		},
		{
			// 30 local dates including today: 2026-09-28 .. 2026-10-27.
			name:   "Helsinki month across the fall back",
			zone:   "Europe/Helsinki",
			period: "month",
			now:    "2026-10-27 12:00",
			readings: []string{
				"2026-09-27 23:30", // before the first date (09-28)
				"2026-09-28 00:30", "2026-10-24 00:30", "2026-10-24 23:30", "2026-10-25 00:30",
				"2026-10-27 00:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-10-27", Count: 1},
				{Date: "2026-10-25", Count: 1},
				{Date: "2026-10-24", Count: 2},
				{Date: "2026-09-28", Count: 1},
			},
		},
		{
			// Midnight of 2026-03-08 does not exist in Havana: the day starts at 01:00. Week is
			// 2026-03-04 .. 2026-03-10.
			name:   "Havana week across the skipped midnight",
			zone:   "America/Havana",
			period: "week",
			now:    "2026-03-10 12:00",
			readings: []string{
				"2026-03-03 23:30", // before the first date (03-04)
				"2026-03-07 23:30", "2026-03-08 01:30", "2026-03-08 23:30", "2026-03-09 00:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-03-09", Count: 1},
				{Date: "2026-03-08", Count: 2},
				{Date: "2026-03-07", Count: 1},
			},
		},
		{
			// The day period is today's local date: the skipped-midnight day starts at 01:00, and
			// the elapsed 24 hours would reach back into 03-07.
			name:   "Havana day on the skipped midnight",
			zone:   "America/Havana",
			period: "day",
			now:    "2026-03-08 12:00",
			readings: []string{
				"2026-03-07 23:30", "2026-03-08 01:30", "2026-03-08 11:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-03-08", Count: 2},
			},
		},
		{
			// Unknown periods behave like "day", the repository's existing default.
			name:   "Helsinki unknown period is the current day",
			zone:   "Europe/Helsinki",
			period: "year",
			now:    "2026-10-27 12:00",
			readings: []string{
				"2026-10-26 23:30", "2026-10-27 00:30", "2026-10-27 11:30",
			},
			want: []datastore.DailyAnalyticsData{
				{Date: "2026-10-27", Count: 2},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loc := toolchainZone(t, tt.zone)
			ds, cleanup := setupTestDatastore(t)
			defer cleanup()
			ds.timezone = loc
			for _, reading := range tt.readings {
				seedDetection(t, ds, trendsSpecies, trendInstant(t, loc, reading))
			}

			got, err := ds.detectionTrends(t.Context(), tt.period, trendInstant(t, loc, tt.now))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestV2OnlyDatastore_GetDetectionTrendsKeepsResponseShape pins the public method: dates are
// YYYY-MM-DD strings, newest first, and an empty result is a non-nil list and a detection from now is counted on today's date.
func TestV2OnlyDatastore_GetDetectionTrendsKeepsResponseShape(t *testing.T) {
	t.Parallel()

	loc := toolchainZone(t, "Europe/Helsinki")
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.timezone = loc

	empty, err := ds.GetDetectionTrends(t.Context(), "week", 0)
	require.NoError(t, err)
	assert.NotNil(t, empty, "no detections is an empty list, not null")
	assert.Empty(t, empty)

	now := time.Now().In(loc)
	seedDetection(t, ds, trendsSpecies, now.Add(-time.Second))

	got, err := ds.GetDetectionTrends(t.Context(), "week", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, now.Add(-time.Second).In(loc).Format(time.DateOnly), got[0].Date)
	assert.Equal(t, 1, got[0].Count)
}
