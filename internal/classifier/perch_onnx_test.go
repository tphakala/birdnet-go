package classifier

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
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
			for _, calibrated := range []bool{false, true} {
				p := &Perch{
					classifier: &predTelemetryClassifier{logits: tt.logits},
					labels:     []string{"A", "B", "C"},
					backend:    BackendOpenVINO,
					device:     "GPU",
					calibrated: calibrated,
					restLogit:  testRestLogit,
				}
				results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
				require.Error(t, err, "non-finite logits must fail the prediction (calibrated=%v)", calibrated)
				assert.Nil(t, results, "no results may be returned for a poisoned window")
				assert.Contains(t, err.Error(), "non-finite score")
			}
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

// testRestLogit is a typical fitted rest logit, used where the exact tile does not matter.
const testRestLogit = 9.75

func TestPerchSoftmaxWithRest(t *testing.T) {
	t.Parallel()

	sum := func(v []float32) float64 {
		var total float64
		for _, x := range v {
			total += float64(x)
		}
		return total
	}

	t.Run("rest far below every logit equals perchSoftmax", func(t *testing.T) {
		t.Parallel()
		logits := []float32{1.5, -0.3, 4.2, 0.1}
		got := perchSoftmaxWithRest(logits, -200)
		want := perchSoftmax(logits)
		require.Len(t, got, len(want))
		for i := range want {
			assert.InDelta(t, want[i], got[i], 1e-6, "class %d", i)
		}
	})

	t.Run("rest far above every logit gives finite non-negative scores", func(t *testing.T) {
		t.Parallel()
		for _, rest := range []float64{100, 1e4, 1e30} {
			got := perchSoftmaxWithRest([]float32{0.2, -0.1, 0.05}, rest)
			require.Len(t, got, 3)
			for i, v := range got {
				assert.False(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "rest %v class %d is %v", rest, i, v)
				assert.GreaterOrEqual(t, v, float32(0), "rest %v class %d", rest, i)
				assert.Less(t, v, float32(1e-6), "rest %v class %d", rest, i)
			}
		}
	})

	t.Run("huge logits do not overflow", func(t *testing.T) {
		t.Parallel()
		got := perchSoftmaxWithRest([]float32{3e4, 1}, testRestLogit)
		assert.InDelta(t, 1.0, got[0], 1e-6)
		assert.InDelta(t, 0.0, got[1], 1e-6)
	})

	t.Run("empty input gives empty output", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, perchSoftmaxWithRest(nil, testRestLogit))
		assert.Empty(t, perchSoftmaxWithRest([]float32{}, testRestLogit))
	})

	t.Run("single class", func(t *testing.T) {
		t.Parallel()
		got := perchSoftmaxWithRest([]float32{testRestLogit}, testRestLogit)
		require.Len(t, got, 1)
		assert.InDelta(t, 0.5, got[0], 1e-6, "a class at the rest logit takes half the mass")
	})

	t.Run("ordinary case sums to one minus the rest share and keeps the order", func(t *testing.T) {
		t.Parallel()
		logits := []float32{9.0, 11.2, 7.5, 10.1}
		got := perchSoftmaxWithRest(logits, testRestLogit)
		var denom float64
		for _, z := range logits {
			denom += math.Exp(float64(z))
		}
		denom += math.Exp(testRestLogit)
		pRest := math.Exp(testRestLogit) / denom
		assert.InDelta(t, 1-pRest, sum(got), 1e-6)
		assert.InDelta(t, math.Exp(11.2)/denom, got[1], 1e-6)

		plain := perchSoftmax(logits)
		for i := range logits {
			for j := range logits {
				assert.Equal(t, plain[i] > plain[j], got[i] > got[j], "order of %d vs %d", i, j)
			}
		}
	})
}

// TestPerchPredict_CalibratedUsesRestTerm checks Predict picks the rest-aware
// softmax only for a calibrated instance, and that the uncalibrated path is the
// plain softmax.
func TestPerchPredict_CalibratedUsesRestTerm(t *testing.T) {
	t.Parallel()

	logits := []float32{10.4, 8.0, 6.5}
	labels := []string{"A", "B", "C"}
	predict := func(p *Perch) float32 {
		results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
		require.NoError(t, err)
		require.NotEmpty(t, results)
		assert.Equal(t, "A", results[0].Species)
		return results[0].Confidence
	}

	plain := predict(&Perch{classifier: &predTelemetryClassifier{logits: logits}, labels: labels})
	assert.InDelta(t, perchSoftmax(logits)[0], plain, 0)

	calibrated := predict(&Perch{classifier: &predTelemetryClassifier{logits: logits}, labels: labels, calibrated: true, restLogit: testRestLogit})
	assert.InDelta(t, perchSoftmaxWithRest(logits, testRestLogit)[0], calibrated, 0)
	assert.Less(t, calibrated, plain, "the rest term must lower the score")
}

// perchSliceFixture is the JSON header of testdata/perch/<tile>_windows.bin.
type perchSliceFixture struct {
	Tile    string    `json:"tile"`
	Classes int       `json:"classes"`
	Windows int       `json:"windows"`
	FullLSE []float64 `json:"full_lse"`
}

// TestPerchPredict_SliceScoresTrackFullModel runs real slice logits (a gather of the
// full model's) through Predict with the catalog's calibration and compares the
// top score with the full-model score exp(z - lse_full). Without the rest term the
// ratio sits at 1.5 to 2.1 (median); with it the median must land near 1. Fixture
// provenance: testdata/perch/README.md.
func TestPerchPredict_SliceScoresTrackFullModel(t *testing.T) {
	t.Parallel()

	const (
		minRatio       = 0.7
		maxRatio       = 1.6
		minMedianRatio = 0.85
		maxMedianRatio = 1.15
	)
	for _, tile := range []string{"nordic", "north-america-east"} {
		t.Run(tile, func(t *testing.T) {
			t.Parallel()
			header, err := os.ReadFile(filepath.Join("testdata", "perch", tile+"_windows.json")) //nolint:gosec // fixed testdata path
			require.NoError(t, err)
			var fx perchSliceFixture
			require.NoError(t, json.Unmarshal(header, &fx))
			require.Equal(t, tile, fx.Tile)
			windows := readPerchLogitsFixture(t, tile+"_windows.bin", fx.Classes)
			require.Len(t, windows, fx.Windows)
			require.Len(t, fx.FullLSE, fx.Windows)

			cal := embeddedScoreCalibration(t, "no-dft-fp32@"+tile)
			p := &Perch{labels: syntheticPerchLabels(fx.Classes), calibrated: true, restLogit: cal.RestLogit}

			ratios := make([]float64, 0, len(windows))
			plainRatios := make([]float64, 0, len(windows))
			for w, logits := range windows {
				p.classifier = &predTelemetryClassifier{logits: logits}
				results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
				require.NoError(t, err)
				top := float64(slices.Max(logits))
				full := math.Exp(top - fx.FullLSE[w])
				ratios = append(ratios, float64(results[0].Confidence)/full)
				plainRatios = append(plainRatios, float64(perchSoftmax(logits)[slices.Index(logits, float32(top))])/full)
			}
			for w, r := range ratios {
				assert.GreaterOrEqual(t, r, minRatio, "window %d", w)
				assert.LessOrEqual(t, r, maxRatio, "window %d", w)
			}
			slices.Sort(ratios)
			slices.Sort(plainRatios)
			median := ratios[len(ratios)/2]
			assert.GreaterOrEqual(t, median, minMedianRatio)
			assert.LessOrEqual(t, median, maxMedianRatio)
			assert.Greater(t, plainRatios[len(plainRatios)/2], maxMedianRatio, "fixture must show the inflation the calibration removes")
		})
	}
}

// embeddedScoreCalibration returns the built-in catalog's calibration for a Perch v2 variant id.
func embeddedScoreCalibration(t *testing.T, variantID string) ScoreCalibration {
	t.Helper()
	for i := range EmbeddedCatalog {
		if EmbeddedCatalog[i].RegistryID != RegistryIDPerchV2 {
			continue
		}
		for j := range EmbeddedCatalog[i].Variants {
			if v := &EmbeddedCatalog[i].Variants[j]; v.ID == variantID {
				require.NotNil(t, v.ScoreCalibration, "variant %s has no calibration", variantID)
				return *v.ScoreCalibration
			}
		}
	}
	require.FailNow(t, "variant not in the built-in catalog", variantID)
	return ScoreCalibration{}
}
