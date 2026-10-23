package classifier

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/onnx"
)

// Environment variables that enable the real-model heatmap test. The model and
// label variables are shared with the range filter integration tests in
// internal/inference/onnx.
const (
	envHeatmapGeomodel = "BIRDNET_GEOMODEL_PATH"
	envHeatmapLabels   = "BIRDNET_GEOMODEL_LABELS_PATH"
	envHeatmapORTLib   = "ONNX_TEST_ORT_LIB"

	// heatmapScoreTolerance allows for the different thread counts of the
	// heatmap session (4 intra-op) and the range filter session (1).
	heatmapScoreTolerance = 1e-5
)

// TestHeatmapService_ComputeGridMatchesRangeFilter runs the heatmap grid
// computation through its IOBinding path, including a partial last chunk that
// is rebound, and compares every cell and week with the range filter run on the
// same inputs. It needs the geomodel, so it is opt-in through
// BIRDNET_GEOMODEL_PATH and BIRDNET_GEOMODEL_LABELS_PATH.
func TestHeatmapService_ComputeGridMatchesRangeFilter(t *testing.T) {
	modelPath := os.Getenv(envHeatmapGeomodel)
	labelsPath := os.Getenv(envHeatmapLabels)
	if modelPath == "" || labelsPath == "" {
		t.Skipf("set %s and %s to run", envHeatmapGeomodel, envHeatmapLabels)
	}
	require.NoError(t, inference.InitONNXRuntime(os.Getenv(envHeatmapORTLib)))

	labels, err := onnx.LoadLabels(labelsPath)
	require.NoError(t, err)

	svc, err := NewHeatmapInferenceService(modelPath, labels)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, svc.Close()) })

	rf, err := onnx.NewRangeFilter(modelPath, onnx.WithRangeFilterLabels(labels))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, rf.Close()) })

	const (
		totalCells = 5 // two full chunks of 2 and a partial chunk of 1
		batchSize  = 2
		stride     = 20
		totalWeeks = 48
		speciesIdx = 3
	)
	coords := []float32{60.1, 24.9, 52.5, 13.4, 40.7, -74.0, -33.9, 151.2, 35.7, 139.7}
	weeks := []int{1, 21, 41}
	result := make([]float32, len(weeks)*totalCells)

	err = svc.ComputeGridWithBinding(t.Context(), coords, totalCells, labels[speciesIdx], stride, totalWeeks, batchSize, result)
	require.NoError(t, err)

	for weekIdx, week := range weeks {
		inputs := make([]float32, 0, totalCells*3)
		for cell := range totalCells {
			inputs = append(inputs, coords[cell*2], coords[cell*2+1], float32(week))
		}
		scores, err := rf.PredictBatchRaw(inputs, totalCells)
		require.NoError(t, err)
		for cell := range totalCells {
			assert.InDelta(t, scores[cell*len(labels)+speciesIdx], result[weekIdx*totalCells+cell], heatmapScoreTolerance,
				"week %d cell %d", week, cell)
		}
	}
}
