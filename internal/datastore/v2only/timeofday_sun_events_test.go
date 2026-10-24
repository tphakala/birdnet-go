package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Helsinki in early summer has a long day with distinct sunrise and sunset
// transitions, so all four periods are populated within a few days.
const (
	helsinkiLatitude  = 60.1699
	helsinkiLongitude = 24.9384
	// seedStep spaces the seeded detections so every sun-event window is hit.
	seedStep = 20 * time.Minute
	// seedDays is how many consecutive days are seeded.
	seedDays = 3
	// searchLimit comfortably exceeds the seeded row count.
	searchLimit = 5000
)

// TestV2OnlyDatastore_TimeOfDayFilterMatchesTheLabel pins that the time-of-day
// filter resolves against real sun events and returns exactly the detections the
// per-row label calls that period, with or without a date range.
func TestV2OnlyDatastore_TimeOfDayFilterMatchesTheLabel(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.suncalc = suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)

	start := time.Date(2024, 6, 10, 0, 0, 0, 0, time.UTC)
	labelByID := make(map[uint]string)
	instantByID := make(map[uint]time.Time)
	var all []time.Time
	for ts := start; ts.Before(start.AddDate(0, 0, seedDays)); ts = ts.Add(seedStep) {
		det := seedDetection(t, ds, "Turdus merula", ts)
		labelByID[det.ID] = ds.calculateTimeOfDay(ts, 1, 1)
		instantByID[det.ID] = ts
		all = append(all, ts)
	}

	periods := []string{
		datastore.TimeOfDaySunrise, datastore.TimeOfDayDay,
		datastore.TimeOfDaySunset, datastore.TimeOfDayNight,
	}

	idsFor := func(t *testing.T, filters *datastore.AdvancedSearchFilters) []uint {
		t.Helper()
		filters.Limit = searchLimit
		notes, _, err := ds.SearchNotesAdvanced(filters)
		require.NoError(t, err)
		ids := make([]uint, 0, len(notes))
		for i := range notes {
			ids = append(ids, notes[i].ID)
		}
		return ids
	}
	wantIDs := func(period string) []uint {
		var ids []uint
		for id, label := range labelByID {
			if label == period {
				ids = append(ids, id)
			}
		}
		return ids
	}

	for _, period := range periods {
		t.Run(period+" without a date range", func(t *testing.T) {
			want := wantIDs(period)
			require.NotEmpty(t, want, "seed must cover %s", period)
			got := idsFor(t, &datastore.AdvancedSearchFilters{TimeOfDay: []string{period}})
			assert.ElementsMatch(t, want, got)
		})
	}

	t.Run("an open-ended wide range gives the same answer as no range", func(t *testing.T) {
		got := idsFor(t, &datastore.AdvancedSearchFilters{
			TimeOfDay: []string{datastore.TimeOfDayNight},
			DateRange: &datastore.DateRange{
				Start: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
				End:   time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		})
		assert.ElementsMatch(t, wantIDs(datastore.TimeOfDayNight), got)
	})

	t.Run("a date range narrows the sun-event answer", func(t *testing.T) {
		day := start.AddDate(0, 0, 1)
		got := idsFor(t, &datastore.AdvancedSearchFilters{
			TimeOfDay: []string{datastore.TimeOfDayDay},
			DateRange: &datastore.DateRange{Start: day, End: day},
		})
		var want []uint
		for id, label := range labelByID {
			if label == datastore.TimeOfDayDay && instantByID[id].Format(time.DateOnly) == day.Format(time.DateOnly) {
				want = append(want, id)
			}
		}
		require.NotEmpty(t, want)
		assert.ElementsMatch(t, want, got)
	})

	t.Run("several periods are unioned", func(t *testing.T) {
		got := idsFor(t, &datastore.AdvancedSearchFilters{
			TimeOfDay: []string{datastore.TimeOfDaySunrise, datastore.TimeOfDaySunset},
		})
		want := append(wantIDs(datastore.TimeOfDaySunrise), wantIDs(datastore.TimeOfDaySunset)...)
		assert.ElementsMatch(t, want, got)
	})

	t.Run("every period together covers every seeded row", func(t *testing.T) {
		got := idsFor(t, &datastore.AdvancedSearchFilters{TimeOfDay: periods})
		assert.Len(t, got, len(all))
	})
}

// TestV2OnlyDatastore_TimeOfDayFilterOnEmptyStore pins that an empty table has no
// windows and returns nothing without error.
func TestV2OnlyDatastore_TimeOfDayFilterOnEmptyStore(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.suncalc = suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)

	notes, total, err := ds.SearchNotesAdvanced(&datastore.AdvancedSearchFilters{
		TimeOfDay: []string{datastore.TimeOfDayDay},
		Limit:     searchLimit,
	})
	require.NoError(t, err)
	assert.Empty(t, notes)
	assert.Zero(t, total)
}

// TestV2OnlyDatastore_ConfidenceBand pins the inclusive confidence band end to
// end, including a maximum of zero being a real cap and not "no maximum".
func TestV2OnlyDatastore_ConfidenceBand(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	at := time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)
	confidences := []float64{0.10, 0.40, 0.70, 0.95}
	byConfidence := make(map[float64]uint, len(confidences))
	for i, c := range confidences {
		det := seedDetection(t, ds, "Turdus merula", at.Add(time.Duration(i)*time.Minute))
		require.NoError(t, ds.manager.DB().Model(det).Update("confidence", c).Error)
		byConfidence[c] = det.ID
	}

	idsFor := func(band *datastore.ConfidenceRangeFilter) []uint {
		t.Helper()
		notes, _, err := ds.SearchNotesAdvanced(&datastore.AdvancedSearchFilters{ConfidenceRange: band, Limit: searchLimit})
		require.NoError(t, err)
		ids := make([]uint, 0, len(notes))
		for i := range notes {
			ids = append(ids, notes[i].ID)
		}
		return ids
	}

	t.Run("band is inclusive on both ends", func(t *testing.T) {
		got := idsFor(&datastore.ConfidenceRangeFilter{Min: 0.40, Max: 0.70})
		assert.ElementsMatch(t, []uint{byConfidence[0.40], byConfidence[0.70]}, got)
	})

	t.Run("no upper bound", func(t *testing.T) {
		got := idsFor(&datastore.ConfidenceRangeFilter{Min: 0.70, Max: datastore.ConfidenceUpperBound})
		assert.ElementsMatch(t, []uint{byConfidence[0.70], byConfidence[0.95]}, got)
	})

	t.Run("a maximum of zero matches nothing", func(t *testing.T) {
		assert.Empty(t, idsFor(&datastore.ConfidenceRangeFilter{Min: 0, Max: 0}))
	})
}

// TestV2OnlyDatastore_TimeOfDayFilterIgnoresImplausiblyOldRows pins that a row
// stamped at the Unix epoch (a zeroed clock) does not stretch the span the sun
// events are resolved over back to 1970, which would be twenty thousand days of
// windows for a request about last week.
func TestV2OnlyDatastore_TimeOfDayFilterIgnoresImplausiblyOldRows(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.suncalc = suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)

	seedDetection(t, ds, "Turdus merula", time.Unix(0, 0))
	noon := time.Date(2024, 6, 10, 9, 0, 0, 0, time.UTC) // midday in Helsinki
	recent := seedDetection(t, ds, "Turdus merula", noon)

	started := time.Now()
	notes, _, err := ds.SearchNotesAdvanced(&datastore.AdvancedSearchFilters{
		TimeOfDay: []string{datastore.TimeOfDayDay},
		Limit:     searchLimit,
	})
	require.NoError(t, err)

	require.Len(t, notes, 1)
	assert.Equal(t, recent.ID, notes[0].ID)
	const generous = 20 * time.Second
	assert.Less(t, time.Since(started), generous, "a bad timestamp must not make the filter walk every day since 1970")
}
