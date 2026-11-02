package classifier

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time check that Perch implements ModelInstance.
var _ ModelInstance = (*Perch)(nil)

// TestPerchPredict_RejectsNonFiniteLogits verifies that a backend returning NaN or
// Inf logits fails the window instead of feeding softmax, which would otherwise
// turn every score into NaN and let the results bypass the confidence threshold
// (NaN compares false against any threshold). Regression for the OpenVINO f16
// Perch path on Intel Arc GPUs, which returns all-NaN logits.
func TestPerchPredict_RejectsNonFiniteLogits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		logits []float32
	}{
		{name: "NaN", logits: []float32{0.2, float32(math.NaN()), 0.1}},
		{name: "+Inf", logits: []float32{float32(math.Inf(1)), 0.2, 0.1}},
		{name: "-Inf", logits: []float32{0.2, 0.1, float32(math.Inf(-1))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Perch{
				classifier: &predTelemetryClassifier{logits: tt.logits},
				labels:     []string{"A", "B", "C"},
				backend:    BackendOpenVINO,
				device:     "GPU",
			}
			results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
			require.Error(t, err, "non-finite logits must fail the prediction")
			assert.Nil(t, results, "no results may be returned for a poisoned window")
			assert.Contains(t, err.Error(), "non-finite score")
		})
	}
}

// TestPerchSoftmax_NonFinitePoisonsAllScores documents why the guard in
// Perch.Predict sits before softmax: a single NaN logit poisons every output
// score.
func TestPerchSoftmax_NonFinitePoisonsAllScores(t *testing.T) {
	t.Parallel()
	out := perchSoftmax([]float32{0.2, float32(math.NaN()), 0.1})
	for i, v := range out {
		assert.True(t, math.IsNaN(float64(v)), "score %d must be NaN once any logit is NaN", i)
	}
}

// perchGoldenWindows is the number of windows in testdata/perch/full_logits.bin:
// digital silence, a night background window and a bird window.
const perchGoldenWindows = 3

// perchFullClassCount is the number of classes of the full Perch v2 head.
const perchFullClassCount = 14795

// perchGoldenFile is the golden score file for TestPerchPredict_FullModelScoresGolden.
const perchGoldenFile = "testdata/perch/full_scores.golden.json"

// perchGoldenResult is one top-K entry of the golden file. Bits carries
// math.Float32bits of the confidence so the comparison is bit-exact.
type perchGoldenResult struct {
	Species    string  `json:"species"`
	Confidence float32 `json:"confidence"`
	Bits       uint32  `json:"bits"`
}

// syntheticPerchLabels returns n distinct labels. The golden test pins scores, not
// label parsing, so it needs no real label file.
func syntheticPerchLabels(n int) []string {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = fmt.Sprintf("Species_%05d", i)
	}
	return labels
}

// readPerchLogitsFixture reads float32 little-endian logits from a testdata file.
func readPerchLogitsFixture(t *testing.T, name string, classes int) [][]float32 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "perch", name)) //nolint:gosec // fixed testdata path
	require.NoError(t, err)
	rowBytes := classes * 4
	require.Zero(t, len(raw)%rowBytes, "%s is not a whole number of %d-class rows", name, classes)
	rows := make([][]float32, len(raw)/rowBytes)
	for r := range rows {
		rows[r] = make([]float32, classes)
		for i := range rows[r] {
			rows[r][i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[r*rowBytes+i*4:]))
		}
	}
	return rows
}

// TestPerchPredict_FullModelScoresGolden pins the full-model Perch path bit for bit:
// real full-model logits (see testdata/perch/README.md) must keep producing the
// recorded top-K confidences. Any change to the full-model formula, float width or
// accumulation order changes every full-model user's thresholds, dynamic-threshold
// state and filters, so it fails here. Regenerate deliberately with
// UPDATE_PERCH_GOLDEN=1.
func TestPerchPredict_FullModelScoresGolden(t *testing.T) {
	t.Parallel()

	windows := readPerchLogitsFixture(t, "full_logits.bin", perchFullClassCount)
	require.Len(t, windows, perchGoldenWindows)
	labels := syntheticPerchLabels(perchFullClassCount)

	got := make([][]perchGoldenResult, len(windows))
	for w, logits := range windows {
		p := &Perch{classifier: &predTelemetryClassifier{logits: logits}, labels: labels}
		results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
		require.NoError(t, err)
		for _, r := range results {
			got[w] = append(got[w], perchGoldenResult{Species: r.Species, Confidence: r.Confidence, Bits: math.Float32bits(r.Confidence)})
		}
	}
	data, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')

	if os.Getenv("UPDATE_PERCH_GOLDEN") != "" {
		require.NoError(t, os.WriteFile(perchGoldenFile, data, 0o600))
		return
	}
	want, err := os.ReadFile(perchGoldenFile)
	require.NoError(t, err, "golden missing; regenerate with UPDATE_PERCH_GOLDEN=1")
	assert.Equal(t, string(want), string(data), "full-model Perch scores changed")
}
