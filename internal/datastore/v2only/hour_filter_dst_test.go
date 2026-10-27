package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestV2OnlyDatastore_HourFilterMatchesDashboardOnDSTDays pins that, for every hour of a DST
// change day, the hour filters of the search agree with the dashboard's hourly cell.
func TestV2OnlyDatastore_HourFilterMatchesDashboardOnDSTDays(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	assertHourFilterMatchesDashboardOnDSTDays(t, ds)
}

// assertHourFilterMatchesDashboardOnDSTDays seeds detections around both 2026 Europe/Helsinki
// DST changes into an empty datastore and checks, per day and hour, that GetBatchHourlyOccurrences,
// the advanced hour filter and the simple time-of-day filter agree with the expected counts.
// The SQLite and MySQL tests share it.
func assertHourFilterMatchesDashboardOnDSTDays(t *testing.T, ds *Datastore) {
	t.Helper()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki
	ctx := t.Context()

	const species = "Fringilla coelebs"
	utc := func(month time.Month, day, hour, minute, second int) time.Time {
		return time.Date(2026, month, day, hour, minute, second, 0, time.UTC)
	}
	seeds := []time.Time{
		// The second before 2026-03-29 00:00 EET: outside the day.
		utc(time.March, 28, 21, 59, 59),
		// Spring forward at 01:00Z: 03:xx does not exist.
		utc(time.March, 29, 0, 59, 59),
		utc(time.March, 29, 1, 0, 0),
		utc(time.March, 29, 1, 15, 0),
		// The last second of the day, and the next local midnight: outside the day.
		utc(time.March, 29, 20, 59, 59),
		utc(time.March, 29, 21, 0, 0),
		// The last second of the day before 2026-10-25: outside the day.
		utc(time.October, 24, 20, 59, 59),
		// Fall back at 01:00Z: 03:xx happens twice.
		utc(time.October, 24, 23, 50, 0),
		utc(time.October, 25, 0, 10, 0),
		utc(time.October, 25, 0, 59, 59),
		utc(time.October, 25, 1, 0, 0),
		utc(time.October, 25, 1, 40, 0),
		utc(time.October, 25, 2, 5, 0),
		utc(time.October, 25, 21, 15, 0),
		utc(time.October, 25, 21, 59, 59),
		// The next local midnight: outside the day.
		utc(time.October, 25, 22, 0, 0),
	}
	for _, at := range seeds {
		seedDetection(t, ds, species, at)
	}

	days := []struct {
		date string
		want [24]int
	}{
		{"2026-03-29", hourCounts(map[int]int{2: 1, 4: 2, 23: 1})},
		{"2026-10-25", hourCounts(map[int]int{2: 1, 3: 4, 4: 1, 23: 2})},
	}
	// The simple time-of-day "night" is hours 20-23 and 0-4.
	nightHours := []int{20, 21, 22, 23, 0, 1, 2, 3, 4}

	for i := range days {
		day := &days[i]
		dayStart, err := time.ParseInLocation(time.DateOnly, day.date, helsinki)
		require.NoError(t, err)

		batch, err := ds.GetBatchHourlyOccurrences(ctx, day.date, day.date, []string{species}, 0)
		require.NoError(t, err)
		assert.Equal(t, day.want, batch[species], "%s dashboard cells", day.date)

		for hour, want := range day.want {
			_, total, err := ds.SearchNotesAdvanced(&datastore.AdvancedSearchFilters{
				DateRange: &datastore.DateRange{Start: dayStart, End: dayStart},
				Hour:      &datastore.HourFilter{Start: hour, End: hour},
				Species:   []string{species},
			})
			require.NoError(t, err)
			assert.Equal(t, int64(want), total, "%s hour %d advanced filter", day.date, hour)
		}

		wantNight := 0
		for _, hour := range nightHours {
			wantNight += day.want[hour]
		}
		_, total, err := ds.SearchDetections(&datastore.SearchFilters{
			DateStart: day.date,
			DateEnd:   day.date,
			TimeOfDay: "night",
		})
		require.NoError(t, err)
		assert.Equal(t, wantNight, total, "%s simple night filter", day.date)
	}
}

// hourCounts expands a sparse hour to count map into the dashboard's 24 cells.
func hourCounts(counts map[int]int) [24]int {
	var cells [24]int
	for hour, n := range counts {
		cells[hour] = n
	}
	return cells
}
