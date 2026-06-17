//go:build openvino

package openvino

import (
	"math"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// finiteSampleCount is how many leading output logits each round-trip test spot-
// checks for NaN/Inf.
const finiteSampleCount = 8

// Input/output dimensions of the BirdNET v2.4 classifier (named to avoid magic numbers).
const (
	birdNETInputSamples  = 144000
	birdNETOutputClasses = 6522
)

// Input/output dimensions of the Perch v2 (no_dft) classifier. Its species logits
// are the 4th output (index 3); index 0 is the 1536-d embedding.
const (
	perchInputSamples      = 160000
	perchOutputClasses     = 14795
	perchLogitsOutputIndex = 3
	perchEmbeddingClasses  = 1536
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
	for i, v := range out[:finiteSampleCount] {
		require.Falsef(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "out[%d] not finite: %v", i, v)
	}
}

// TestOpenVINOPerchRoundTrip is a hardware/lib-gated smoke test for the Perch v2
// multi-output graph. Set OV_TEST_PERCH_MODEL to a perch_v2_no_dft ONNX path to
// run it. OV_TEST_DEVICE selects the device ("CPU" default, or "GPU"); a GPU run
// is skipped if no GPU is enumerated. It proves the OutputIndex selection (logits
// at index 3, not the index-0 embedding) and that NumClasses reports the real
// model output dimension.
func TestOpenVINOPerchRoundTrip(t *testing.T) {
	model := os.Getenv("OV_TEST_PERCH_MODEL")
	if model == "" {
		t.Skip("set OV_TEST_PERCH_MODEL to a perch_v2_no_dft ONNX to run the Perch OpenVINO round-trip")
	}
	require.NoError(t, InitOV(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOV() })

	device := os.Getenv("OV_TEST_DEVICE")
	if device == "" {
		device = DeviceCPU
	}
	if device == DeviceGPU {
		devs, err := AvailableDevices()
		require.NoError(t, err)
		if !slices.Contains(devs, DeviceGPU) {
			t.Skipf("no GPU device enumerated (have %v)", devs)
		}
	}

	c, err := NewClassifier(model, Options{
		PrecisionHint: DefaultPrecisionHint,
		Device:        device,
		OutputIndex:   perchLogitsOutputIndex,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	// NumClasses must reflect the index-3 logits output (14795), not the labels
	// or the index-0 embedding (1536).
	require.Equal(t, perchOutputClasses, c.NumClasses())

	out, err := c.PredictRaw(make([]float32, perchInputSamples))
	require.NoError(t, err)
	require.Len(t, out, perchOutputClasses)
	require.NotEqual(t, perchEmbeddingClasses, len(out), "output must be logits (idx 3), not the embedding (idx 0)")

	for i, v := range out[:finiteSampleCount] {
		require.Falsef(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "out[%d] not finite: %v", i, v)
	}
}

// TestOpenVINOAvailableDevices verifies device enumeration returns at least the
// CPU device once the core is loaded. Lib-gated via OV_TEST_LIB / standard paths.
func TestOpenVINOAvailableDevices(t *testing.T) {
	if os.Getenv("OV_TEST_MODEL") == "" && os.Getenv("OV_TEST_PERCH_MODEL") == "" {
		t.Skip("set OV_TEST_MODEL or OV_TEST_PERCH_MODEL to run device enumeration (needs libopenvino_c)")
	}
	require.NoError(t, InitOV(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOV() })

	devs, err := AvailableDevices()
	require.NoError(t, err)
	assert.Contains(t, devs, DeviceCPU, "CPU device must always be enumerated")
	t.Logf("OpenVINO devices: %v", devs)
}
