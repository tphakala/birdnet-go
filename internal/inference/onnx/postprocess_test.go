package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestActivationFor_BirdNETv30Passthrough is the regression guard for the v3.0
// double-sigmoid bug: v3.0 applies its per-class sigmoid in-graph, so the
// predictions output must be passed through unchanged, not re-activated.
func TestActivationFor_BirdNETv30Passthrough(t *testing.T) {
	t.Parallel()

	probs := []float32{0.9, 0.01, 0.3, 0.0, 1.0}
	got := activationFor(BirdNETv30, probs)

	require.Len(t, got, len(probs))
	for i := range probs {
		assert.InDelta(t, probs[i], got[i], 1e-9,
			"v3.0 predictions must pass through without re-activation")
	}

	// Must be a distinct slice: the caller destroys the backing tensor.
	require.NotSame(t, &probs[0], &got[0], "activationFor must not alias the input")
	got[0] = -1
	assert.InDelta(t, float32(0.9), probs[0], 0, "mutating the result must not touch the input")
}

// TestActivationFor_BirdNETv24Sigmoid verifies v2.4 still gets a sigmoid.
func TestActivationFor_BirdNETv24Sigmoid(t *testing.T) {
	t.Parallel()

	got := activationFor(BirdNETv24, []float32{0})
	require.Len(t, got, 1)
	assert.InDelta(t, 0.5, got[0], 1e-6, "sigmoid(0) must be 0.5")
}

// TestActivationFor_PerchV2Softmax verifies Perch still gets a softmax (scores
// sum to 1).
func TestActivationFor_PerchV2Softmax(t *testing.T) {
	t.Parallel()

	got := activationFor(PerchV2, []float32{1, 2, 3})
	require.Len(t, got, 3)
	var sum float32
	for _, v := range got {
		sum += v
	}
	assert.InDelta(t, 1.0, sum, 1e-6, "softmax scores must sum to 1")
}
