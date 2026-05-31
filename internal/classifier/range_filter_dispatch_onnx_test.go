//go:build onnx

package classifier

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
)

// geomodelPathsFromEnv returns the geomodel model + labels paths from the standard QA
// environment variables, skipping the test when they are not configured or missing.
func geomodelPathsFromEnv(t *testing.T) (modelPath, labelsPath string) {
	t.Helper()
	modelPath = os.Getenv("BIRDNET_GEOMODEL_PATH")
	labelsPath = os.Getenv("BIRDNET_GEOMODEL_LABELS_PATH")
	if modelPath == "" || labelsPath == "" {
		t.Skip("set BIRDNET_GEOMODEL_PATH and BIRDNET_GEOMODEL_LABELS_PATH to run the geomodel dispatch integration test")
	}
	if _, err := os.Stat(modelPath); err != nil {
		t.Skipf("geomodel not found at %s: %v", modelPath, err)
	}
	if _, err := os.Stat(labelsPath); err != nil {
		t.Skipf("geomodel labels not found at %s: %v", labelsPath, err)
	}
	return modelPath, labelsPath
}

// TestInitializeMetaModel_OrphanGeomodelOnV24 is matrix row A: BirdNET v2.4 with an
// orphaned geomodel config (rangefilter.model="" but modelpath/labelspath still pointing
// at the geomodel). The dispatch fix must route this through the name-matching mapped
// path and produce a working mappedRangeFilter with mappedCount>0, NOT a nil filter
// (silent fail-open) and NOT a downgrade to the embedded TFLite range filter.
func TestInitializeMetaModel_OrphanGeomodelOnV24(t *testing.T) {
	modelPath, labelsPath := geomodelPathsFromEnv(t)

	// Use a subset of the geomodel's own labels as the classifier labels so that
	// scientific-name matching is guaranteed to map at least these species.
	geoLabels, err := onnx.LoadLabels(labelsPath)
	require.NoError(t, err)
	require.NotEmpty(t, geoLabels)
	classifierLabels := geoLabels
	if len(classifierLabels) > 16 {
		classifierLabels = classifierLabels[:16]
	}

	settings := &conf.Settings{}
	settings.BirdNET.Labels = classifierLabels
	settings.BirdNET.RangeFilter.Model = "" // orphan: model cleared, paths left behind
	settings.BirdNET.RangeFilter.ModelPath = modelPath
	settings.BirdNET.RangeFilter.LabelsPath = labelsPath

	bn := &BirdNET{
		Settings:     settings,
		ModelInfo:    ModelRegistry[DefaultModelVersion], // BirdNET v2.4 (TFLite)
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	t.Cleanup(bn.Delete)

	require.NoError(t, bn.initializeMetaModel(settings))

	mapped, ok := bn.rangeFilter.(*mappedRangeFilter)
	require.True(t, ok, "orphan geomodel config must produce a mappedRangeFilter, got %T", bn.rangeFilter)
	assert.Positive(t, mapped.mappedCount, "expected at least one classifier species mapped to the geomodel")
	assert.False(t, bn.rangeFilterFellBack, "must not fall back to embedded TFLite when the geomodel loads")
}
