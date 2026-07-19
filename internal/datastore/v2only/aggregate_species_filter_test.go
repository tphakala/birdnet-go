package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestV2OnlyDatastore_SplitByZoneOffset verifies the hourly range is divided at DST transitions.
//
// SQL hour bucketing applies one fixed UTC offset per query. That is exact for a single day, but
// once the time-of-day chart began requesting a whole range, a span crossing a DST change bucketed
// every detection after the transition an hour out. Splitting lets each part use the offset that
// was actually in effect.
func TestV2OnlyDatastore_SplitByZoneOffset(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)

	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	ds.timezone = newYork

	const (
		estOffset = -5 * 60 * 60 // winter
		edtOffset = -4 * 60 * 60 // summer
	)

	t.Run("range inside one zone period yields a single segment", func(t *testing.T) {
		start := time.Date(2026, 1, 10, 0, 0, 0, 0, newYork).Unix()
		end := time.Date(2026, 1, 20, 0, 0, 0, 0, newYork).Unix()

		segments := ds.splitByZoneOffset(start, end)
		require.Len(t, segments, 1, "no transition in range, so one query")
		assert.Equal(t, estOffset, segments[0].offset)
		assert.Equal(t, start, segments[0].start)
		assert.Equal(t, end, segments[0].end)
	})

	t.Run("range crossing spring-forward splits at the transition", func(t *testing.T) {
		// US DST begins 2026-03-08 02:00 local.
		start := time.Date(2026, 3, 1, 0, 0, 0, 0, newYork).Unix()
		end := time.Date(2026, 3, 15, 0, 0, 0, 0, newYork).Unix()

		segments := ds.splitByZoneOffset(start, end)
		require.Len(t, segments, 2, "one transition splits the range in two")

		assert.Equal(t, estOffset, segments[0].offset, "before the change it is still EST")
		assert.Equal(t, edtOffset, segments[1].offset, "after the change it is EDT")

		// The segments tile the range exactly: no gap, no overlap, no lost detections.
		assert.Equal(t, start, segments[0].start)
		assert.Equal(t, segments[0].end, segments[1].start)
		assert.Equal(t, end, segments[1].end)

		// The split lands on the real transition instant, not a day boundary. Expressed in UTC on
		// purpose: 02:00 local does not exist on this date (clocks jump 02:00 -> 03:00), so building
		// it in New York time normalizes to the wrong side of the transition.
		transition := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).Unix() // 02:00 EST -> 03:00 EDT
		assert.Equal(t, transition, segments[0].end)
	})

	t.Run("a year covers both transitions", func(t *testing.T) {
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, newYork).Unix()
		end := time.Date(2026, 12, 31, 0, 0, 0, 0, newYork).Unix()

		segments := ds.splitByZoneOffset(start, end)
		require.Len(t, segments, 3, "EST -> EDT -> EST")
		assert.Equal(t, []int{estOffset, edtOffset, estOffset},
			[]int{segments[0].offset, segments[1].offset, segments[2].offset})
	})
}

// TestV2OnlyDatastore_HourlyChartsMultiLabelSelection reproduces the "N species selected, fewer
// drawn" bug. A species can own several model labels, and GetTopSpecies groups/limits by label ROW.
// When the caller limits to the selection size, a high-volume species' extra label rows exhaust the
// limit before the lowest-volume selected species is reached, dropping it before the by-species
// merge. An explicit selection must return every selected species regardless of label multiplicity.
func TestV2OnlyDatastore_HourlyChartsMultiLabelSelection(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)
	ds.timezone = time.UTC
	ctx := t.Context()

	const (
		startDate = "2026-03-01"
		endDate   = "2026-03-02"
	)

	// A second model gives one species a second label ID for the same scientific name.
	otherModel, err := ds.model.GetOrCreate(ctx, "Perch", "1.0", "default", entities.ModelTypeBird, nil)
	require.NoError(t, err)

	seedUnderModel := func(sciName string, modelID uint, hour, n int) {
		t.Helper()
		label, err := ds.label.GetOrCreate(ctx, sciName, modelID, ds.speciesLabelTypeID, ds.avesClassID)
		require.NoError(t, err)
		for range n {
			require.NoError(t, ds.detection.Save(ctx, &entities.Detection{
				ModelID:    modelID,
				LabelID:    label.ID,
				DetectedAt: time.Date(2026, 3, 1, hour, 0, 0, 0, time.UTC).Unix(),
				Confidence: 0.9,
			}))
		}
	}

	// Robin owns two labels (default + Perch model), each out-counting the wren, so its two rows fill a
	// limit-by-row before the wren is reached: default=5, perch=4, blackbird=3, wren=2.
	seedUnderModel("Turdus migratorius", ds.defaultModelID, 6, 5)
	seedUnderModel("Turdus migratorius", otherModel.ID, 6, 4)
	seedUnderModel("Turdus merula", ds.defaultModelID, 7, 3)
	seedUnderModel("Troglodytes troglodytes", ds.defaultModelID, 8, 2)

	selection := []string{"Turdus migratorius", "Turdus merula", "Troglodytes troglodytes"}
	namesOf := func(get func() ([]string, error)) []string {
		t.Helper()
		names, err := get()
		require.NoError(t, err)
		return names
	}

	t.Run("ridgeline returns every selected species despite multi-label rows", func(t *testing.T) {
		t.Parallel()
		names := namesOf(func() ([]string, error) {
			got, err := ds.GetHourlyDistributionBySpecies(ctx, startDate, endDate, selection, len(selection))
			out := make([]string, len(got))
			for i, d := range got {
				out[i] = d.ScientificName
			}
			return out, err
		})
		assert.ElementsMatch(t, selection, names)
	})

	t.Run("succession returns every selected species despite multi-label rows", func(t *testing.T) {
		t.Parallel()
		names := namesOf(func() ([]string, error) {
			got, err := ds.GetAcousticSuccession(ctx, startDate, endDate, selection, len(selection))
			out := make([]string, len(got))
			for i, d := range got {
				out[i] = d.ScientificName
			}
			return out, err
		})
		assert.ElementsMatch(t, selection, names)
	})
}

// TestV2OnlyDatastore_HourlyChartsSpeciesFilter verifies that the two top-N-by-volume hour-of-day
// charts (who-sings-when ridgeline and acoustic succession) honor an optional scientific-name
// filter: a non-empty selection restricts the result to those species (still volume-ordered), while
// nil/empty falls back to the top-N default. Both charts share the selectTopSpeciesHourly path, so
// the two subtests pin the same filter behavior for each fold.
func TestV2OnlyDatastore_HourlyChartsSpeciesFilter(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	// t.Cleanup (not defer) so the store stays open until the parallel subtests below finish; a
	// deferred cleanup would close it as soon as this function returns, before they run.
	t.Cleanup(cleanup)
	ds.timezone = time.UTC
	ctx := t.Context()

	const (
		startDate = "2026-03-01"
		endDate   = "2026-03-02"
	)
	// Distinct volumes so the top-N ordering is unambiguous: robin (3) > blackbird (2) > wren (1).
	at := func(hour, n int) []time.Time {
		out := make([]time.Time, n)
		for i := range out {
			out[i] = time.Date(2026, 3, 1, hour, 0, 0, 0, time.UTC)
		}
		return out
	}
	seedAll := func(sciName string, times []time.Time) {
		for _, ts := range times {
			seedDetection(t, ds, sciName, ts)
		}
	}
	seedAll("Turdus migratorius", at(6, 3))
	seedAll("Turdus merula", at(7, 2))
	seedAll("Troglodytes troglodytes", at(8, 1))

	t.Run("GetHourlyDistributionBySpecies filters to the selected species", func(t *testing.T) {
		t.Parallel()
		got, err := ds.GetHourlyDistributionBySpecies(ctx, startDate, endDate, []string{"Turdus migratorius"}, 5)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Turdus migratorius", got[0].ScientificName)
	})

	t.Run("GetHourlyDistributionBySpecies empty selection returns the top-N", func(t *testing.T) {
		t.Parallel()
		got, err := ds.GetHourlyDistributionBySpecies(ctx, startDate, endDate, nil, 5)
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, "Turdus migratorius", got[0].ScientificName) // highest volume ranks first
	})

	t.Run("GetAcousticSuccession filters to the selected species", func(t *testing.T) {
		t.Parallel()
		got, err := ds.GetAcousticSuccession(ctx, startDate, endDate, []string{"Turdus migratorius"}, 6)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Turdus migratorius", got[0].ScientificName)
		assert.Equal(t, 3, got[0].Total)
		assert.Equal(t, 3, got[0].Counts[6])
	})

	t.Run("GetAcousticSuccession empty selection returns the top-N", func(t *testing.T) {
		t.Parallel()
		got, err := ds.GetAcousticSuccession(ctx, startDate, endDate, nil, 6)
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, "Turdus migratorius", got[0].ScientificName)
	})

	t.Run("multi-species selection returns all selected, volume-ordered", func(t *testing.T) {
		t.Parallel()
		got, err := ds.GetAcousticSuccession(ctx, startDate, endDate,
			[]string{"Turdus merula", "Troglodytes troglodytes"}, 6)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "Turdus merula", got[0].ScientificName) // volume 2 outranks wren's 1
		assert.Equal(t, "Troglodytes troglodytes", got[1].ScientificName)
	})
}
