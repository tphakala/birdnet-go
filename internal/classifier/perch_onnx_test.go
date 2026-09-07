package classifier

import (
	"math"
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
