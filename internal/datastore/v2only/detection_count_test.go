package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestV2OnlyDatastore_CountDetectionsMatchListing pins CountSpeciesDetections and
// CountHourlyDetections to the number of rows the matching unpaginated listing
// (SpeciesDetections / GetHourlyDetections with limit 0) returns, so a count and the
// listing it describes always agree. Times are seeded around the
// Europe/Helsinki spring-forward day so the hour and day windows cross a DST change.
func TestV2OnlyDatastore_CountDetectionsMatchListing(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki

	const (
		finch = "Fringilla coelebs"
		robin = "Erithacus rubecula"
	)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2024, time.March, day, hour, minute, 0, 0, helsinki)
	}
	// 2024-03-31 skips 03:00-04:00 local in Helsinki, so that day is 23 hours long. The
	// hour windows are offsets from local midnight (an "hour" of 05 starts 5 hours after
	// midnight, which is 06:00 local on that day).
	seeds := []struct {
		species string
		at      time.Time
	}{
		{finch, at(30, 23, 59)},
		{finch, at(31, 0, 0)},
		{finch, at(31, 2, 30)},
		{finch, at(31, 4, 0)},
		{finch, at(31, 4, 59)},
		{finch, at(31, 6, 15)},
		{finch, at(31, 23, 59)},
		{finch, time.Date(2024, time.April, 1, 0, 0, 0, 0, helsinki)},
		{robin, at(31, 2, 0)},
		{robin, at(31, 6, 0)},
	}
	for _, s := range seeds {
		seedDetection(t, ds, s.species, s.at)
	}

	t.Run("species", func(t *testing.T) {
		tests := []struct {
			name, species, date, hour string
			duration                  int
			want                      int64
		}{
			{"all time", finch, "", "", 0, 8},
			{"whole DST day", finch, "2024-03-31", "", 0, 6},
			{"hour before the gap", finch, "2024-03-31", "02", 1, 1},
			{"window across the gap", finch, "2024-03-31", "02", 3, 3},
			{"zero duration defaults to one hour", finch, "2024-03-31", "03", 0, 2},
			{"other species same day", robin, "2024-03-31", "", 0, 2},
			{"unknown species", "Nonexistent species", "2024-03-31", "", 0, 0},
			{"all species one hour", "", "2024-03-31", "05", 1, 2},
			{"unparsable date means all time", finch, "not-a-date", "", 0, 8},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := ds.CountSpeciesDetections(tt.species, tt.date, tt.hour, tt.duration)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				listed, err := ds.SpeciesDetections(tt.species, tt.date, tt.hour, tt.duration, false, 0, 0)
				require.NoError(t, err)
				assert.Len(t, listed, int(got), "count must match the unpaginated listing")
			})
		}
	})

	t.Run("hourly", func(t *testing.T) {
		tests := []struct {
			name, date, hour string
			duration         int
			want             int64
		}{
			{"one hour", "2024-03-31", "05", 1, 2},
			{"window across the gap", "2024-03-31", "02", 3, 4},
			// The window is 24 elapsed hours from local midnight, and 2024-03-31 is only
			// 23 hours long in Helsinki, so it also takes in the 2024-04-01 00:00 finch:
			// 8 rows on the 31st plus that one.
			{"24 hours from midnight spills into the next day", "2024-03-31", "00", 24, 9},
			{"zero duration is empty", "2024-03-31", "06", 0, 0},
			{"day before", "2024-03-30", "23", 1, 1},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got, err := ds.CountHourlyDetections(tt.date, tt.hour, tt.duration)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				listed, err := ds.GetHourlyDetections(tt.date, tt.hour, tt.duration, 0, 0)
				require.NoError(t, err)
				assert.Len(t, listed, int(got), "count must match the unpaginated listing")
			})
		}
	})

	t.Run("invalid input errors", func(t *testing.T) {
		_, err := ds.CountHourlyDetections("not-a-date", "06", 1)
		require.Error(t, err)
		_, err = ds.CountHourlyDetections("2024-03-31", "xx", 1)
		require.Error(t, err)
		_, err = ds.CountSpeciesDetections(finch, "2024-03-31", "xx", 1)
		require.Error(t, err)
	})
}
