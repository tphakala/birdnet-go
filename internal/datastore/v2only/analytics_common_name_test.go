package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// TestV2OnlyDatastore_ServerChosenSpeciesCarryCommonName pins #4459: analytics rows for
// server-chosen species must carry the server-resolved common name (the same resolver the species
// summary uses), falling back to the scientific name when no mapping exists, so the UI never has to
// look the name up with a second request.
func TestV2OnlyDatastore_ServerChosenSpeciesCarryCommonName(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)
	ds.timezone = time.UTC
	ctx := t.Context()

	const (
		startDate     = "2026-03-01"
		endDate       = "2026-03-02"
		mappedSci     = "Turdus merula"
		mappedCommon  = "Common Blackbird"
		unmappedSci   = "Parus major"
		mappedCount   = 25 // above minConfidenceHistogramDetections so the histogram keeps the species
		unmappedCount = 22
	)

	index := speciesindex.New(fakeResolver{m: map[string]string{mappedSci: mappedCommon}})
	index.Rebuild(nil, "")
	ds.SetSpeciesIndex(index)

	seed := func(sciName string, n int) {
		t.Helper()
		label, err := ds.label.GetOrCreate(ctx, sciName, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
		require.NoError(t, err)
		for range n {
			require.NoError(t, ds.detection.Save(ctx, &entities.Detection{
				ModelID:    ds.defaultModelID,
				LabelID:    label.ID,
				DetectedAt: time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC).Unix(),
				Confidence: 0.9,
			}))
		}
	}
	seed(mappedSci, mappedCount)
	seed(unmappedSci, unmappedCount)

	want := map[string]string{mappedSci: mappedCommon, unmappedSci: unmappedSci}

	t.Run("hourly distribution", func(t *testing.T) {
		rows, err := ds.GetHourlyDistributionBySpecies(ctx, startDate, endDate, nil, 5)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, want[r.ScientificName], r.CommonName, r.ScientificName)
		}
	})

	t.Run("acoustic succession", func(t *testing.T) {
		rows, err := ds.GetAcousticSuccession(ctx, startDate, endDate, nil, 5)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, want[r.ScientificName], r.CommonName, r.ScientificName)
		}
	})

	t.Run("confidence histogram", func(t *testing.T) {
		rows, err := ds.GetConfidenceHistogram(ctx, startDate, endDate, "", 10, 5)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, want[r.ScientificName], r.CommonName, r.ScientificName)
		}
	})

	t.Run("species phenology", func(t *testing.T) {
		rows, err := ds.GetSpeciesPhenology(ctx, startDate, endDate, 5)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		for _, r := range rows {
			assert.Equal(t, want[r.ScientificName], r.CommonName, r.ScientificName)
		}
	})
}
