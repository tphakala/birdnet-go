package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestV2OnlyDatastore_GetTopBirdsData_TaxonomicClass pins the per-species summary note to carry
// the taxonomic class name of the species' label, and to leave it empty when the label has none
// (multi-taxa model labels are stored without a class).
func TestV2OnlyDatastore_GetTopBirdsData_TaxonomicClass(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	t.Cleanup(cleanup)
	ds.timezone = time.UTC
	ctx := t.Context()

	classBySpecies := map[string]*uint{
		"Turdus merula":             ds.avesClassID,
		"Pipistrellus pipistrellus": ds.chiropteraClassID,
		"Vulpes vulpes":             nil,
	}
	for sci, classID := range classBySpecies {
		label, err := ds.label.GetOrCreate(ctx, sci, ds.defaultModelID, ds.speciesLabelTypeID, classID)
		require.NoError(t, err)
		require.NoError(t, ds.detection.Save(ctx, &entities.Detection{
			ModelID:    ds.defaultModelID,
			LabelID:    label.ID,
			DetectedAt: time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC).Unix(),
			Confidence: 0.9,
		}))
	}

	notes, err := ds.GetTopBirdsData(ctx, "2026-03-01", 0, 10)
	require.NoError(t, err)
	require.Len(t, notes, len(classBySpecies))

	got := make(map[string]string, len(notes))
	for i := range notes {
		got[notes[i].ScientificName] = notes[i].TaxonomicClass
	}
	assert.Equal(t, "Aves", got["Turdus merula"])
	assert.Equal(t, "Chiroptera", got["Pipistrellus pipistrellus"])
	assert.Empty(t, got["Vulpes vulpes"], "a label without a class reports no class")
}
