//go:build openvino

package openvino

import (
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Input/output dimensions of the BirdNET v2.4 classifier (named to avoid magic numbers).
const (
	birdNETInputSamples  = 144000
	birdNETOutputClasses = 6522
)

// TestOpenVINORoundTrip is a hardware/lib-gated smoke test. Set OV_TEST_MODEL to a
// BirdNET v2.4 FP32 ONNX path to run it; it is skipped otherwise so normal CI (which
// has no libopenvino_c) stays green. OV_TEST_LIB optionally points at the
// libopenvino_c shared library; empty searches the standard loader paths.
func TestOpenVINORoundTrip(t *testing.T) {
	model := os.Getenv("OV_TEST_MODEL")
	if model == "" {
		t.Skip("set OV_TEST_MODEL to a BirdNET v2.4 FP32 ONNX to run the OpenVINO round-trip")
	}
	require.NoError(t, InitOV(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOV() })

	c, err := NewClassifier(model, Options{PrecisionHint: DefaultPrecisionHint})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	out, err := c.PredictRaw(make([]float32, birdNETInputSamples))
	require.NoError(t, err)
	require.Len(t, out, birdNETOutputClasses)

	// Outputs are raw pre-activation logits (often negative), so do not assert a
	// [0,1] range; just sanity-check that the first few are finite (not NaN/Inf).
	for i, v := range out[:8] {
		require.Falsef(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "out[%d] not finite: %v", i, v)
	}
}
