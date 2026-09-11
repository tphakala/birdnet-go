package classifier

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestModelRegistry_RangeFilterCompat pins the range-filter capability of every
// registry entry and the None fallback for the Custom sentinel, the empty ID and an
// unknown ID. The expected table is exhaustive over ModelRegistry, so adding a model
// without deciding its range-filter compatibility fails this test.
func TestModelRegistry_RangeFilterCompat(t *testing.T) {
	t.Parallel()

	want := map[string]rangeFilterCompat{
		permanentRegistryID: rangeFilterCompatMDataV24,
		RegistryIDBirdNETV3: rangeFilterCompatGeomodel,
		RegistryIDPerchV2:   rangeFilterCompatGeomodel,
		RegistryIDBat:       rangeFilterCompatNone,
		RegistryIDBSG:       rangeFilterCompatNone,
	}
	for id := range ModelRegistry {
		w, ok := want[id]
		require.Truef(t, ok, "registry key %q missing from the expected table; decide its RangeFilterCompat and add a row", id)
		assert.Equalf(t, w, rangeFilterCompatFor(id), "RangeFilterCompat for %q", id)
	}

	// A missing entry yields the zero value (None), never a false auto-selection.
	assert.Equal(t, rangeFilterCompatNone, rangeFilterCompatFor(modelIDCustom), "Custom must never qualify")
	assert.Equal(t, rangeFilterCompatNone, rangeFilterCompatFor(""), "the empty ID must never qualify")
	assert.Equal(t, rangeFilterCompatNone, rangeFilterCompatFor("NoSuchModel"), "an unknown ID must never qualify")
}

// TestShouldAutoSelectV3Geomodel_MatchesLegacyIDSwitch proves the rangeFilterCompat
// gate reproduces the old explicit {Perch_V2, BirdNET_V3.0} string switch exactly, for
// every registry key plus the Custom, empty and unknown sentinels, with the shared
// geomodel files present so only the classifier gate can differ.
func TestShouldAutoSelectV3Geomodel_MatchesLegacyIDSwitch(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, sharedDirName)
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelONNXLocalName), []byte("onnx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelLabelsLocalName), []byte("labels"), 0o644))

	ids := slices.Collect(maps.Keys(ModelRegistry))
	ids = append(ids, modelIDCustom, "", "NoSuchModel")

	for _, id := range ids {
		want := id == RegistryIDPerchV2 || id == RegistryIDBirdNETV3
		assert.Equalf(t, want, shouldAutoSelectV3Geomodel(id, modelsDir),
			"shouldAutoSelectV3Geomodel(%q) must match the legacy {Perch_V2, BirdNET_V3.0} switch", id)
	}
}
