package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
