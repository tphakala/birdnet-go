//go:build openvino

package openvino

import (
	"math"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
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
		if !slices.ContainsFunc(devs, IsGPUDevice) {
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

	// FULL_DEVICE_NAME is supported by every plugin; a failed read here means the
	// ov_core_get_property binding is broken, not that the device lacks a name.
	for _, dev := range devs {
		name, err := DeviceProperty(dev, "FULL_DEVICE_NAME")
		require.NoErrorf(t, err, "FULL_DEVICE_NAME of %s", dev)
		assert.NotEmptyf(t, name, "FULL_DEVICE_NAME of %s", dev)
		devType, typeErr := DeviceProperty(dev, "DEVICE_TYPE")
		t.Logf("%s: name=%q type=%q (type error: %v)", dev, name, devType, typeErr)
	}
}

// TestOpenVINONewClassifierDeviceNames pins which device names NewClassifier
// accepts: CPU, the GPU alias and a concrete GPU.<n>. The model path does not
// exist, so an accepted name fails later, at reading the model, which proves the
// name check let it through. Lib-gated like the other tests that bind the core:
// binding it here unconditionally would make the loader test skip itself.
func TestOpenVINONewClassifierDeviceNames(t *testing.T) {
	if os.Getenv("OV_TEST_MODEL") == "" && os.Getenv("OV_TEST_PERCH_MODEL") == "" {
		t.Skip("set OV_TEST_MODEL or OV_TEST_PERCH_MODEL to run (needs libopenvino_c)")
	}
	require.NoError(t, InitOV(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOV() })

	const (
		unsupported = "unsupported device"
		readModelOp = "ov_core_read_model"
	)
	for _, device := range []string{"", DeviceCPU, DeviceGPU, "GPU.0", "GPU.12"} {
		_, err := NewClassifier("/nonexistent/model.onnx", Options{Device: device})
		require.Errorf(t, err, "device %q", device)
		assert.NotContainsf(t, err.Error(), unsupported, "device %q must pass the name check", device)
		assert.Containsf(t, err.Error(), readModelOp, "device %q must reach the model read", device)
	}
	for _, device := range []string{"NPU", "gpu.1", "GPU.", "GPU.x", "AUTO"} {
		_, err := NewClassifier("/nonexistent/model.onnx", Options{Device: device})
		require.Errorf(t, err, "device %q", device)
		assert.Containsf(t, err.Error(), unsupported, "device %q must be rejected", device)
		assert.Containsf(t, err.Error(), `"GPU.<n>"`, "device %q: the message must show the accepted indexed form", device)
	}
}

// TestScrubLibraryPaths_LongestFirst pins that a resolved absolute path is
// redacted whole even when the configured relative path is its suffix.
func TestScrubLibraryPaths_LongestFirst(t *testing.T) {
	t.Parallel()
	configured := `ov\openvino_c.dll`
	resolved := `C:\Users\someone\birdnet\ov\openvino_c.dll`
	got := scrubLibraryPaths("cannot load "+resolved, configured, resolved)
	assert.Equal(t, "cannot load <path>", got)
	assert.NotContains(t, got, "someone")
}

// resetGPUFaultForTest clears the process-wide latch. Production code has no
// reset path (only a restart clears it); this exists for tests that trip it.
func resetGPUFaultForTest() {
	gpuFault.tripped.Store(false)
	gpuFault.cause.Store(nil)
	gpuFault.streak.Store(0)
}

// TestGPUFaultLatch_RealDevice is a hardware-gated check of the glue between the
// GPU fault latch and the native backend. It needs OV_TEST_MODEL (BirdNET v2.4
// FP32 ONNX), OV_TEST_LIB optionally, and a GPU; it is skipped without a GPU. It
// trips the latch directly (a real device fault cannot be provoked on demand),
// then asserts that GPU calls fail fast with the sentinel, that the CPU device
// keeps working, and that Close and DestroyOV return nil and leave no core
// loaded. Whether they skip the native frees is not observable here (a known gap).
// Not parallel: the latch is process-wide.
func TestGPUFaultLatch_RealDevice(t *testing.T) {
	model := os.Getenv("OV_TEST_MODEL")
	if model == "" {
		t.Skip("set OV_TEST_MODEL to a BirdNET v2.4 FP32 ONNX to run the GPU fault latch test")
	}
	// Registered first so it runs last: the DestroyOV cleanup below must still
	// see the latch set when an assertion fails before the explicit DestroyOV.
	t.Cleanup(resetGPUFaultForTest)
	require.NoError(t, InitOV(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOV() })
	devs, err := AvailableDevices()
	require.NoError(t, err)
	if !slices.ContainsFunc(devs, IsGPUDevice) {
		t.Skipf("no GPU device enumerated (have %v)", devs)
	}

	gpuClassifier, err := NewClassifier(model, Options{PrecisionHint: DefaultPrecisionHint, Device: DeviceGPU})
	require.NoError(t, err)
	silence := make([]float32, birdNETInputSamples)
	_, err = gpuClassifier.PredictRaw(silence)
	require.NoError(t, err, "the GPU classifier works before the latch trips")

	// Unlatched Close frees and clears every field, and a second Close is a no-op.
	spare, err := NewClassifier(model, Options{PrecisionHint: DefaultPrecisionHint, Device: DeviceGPU})
	require.NoError(t, err)
	require.NoError(t, spare.Close())
	require.NoError(t, spare.Close())
	spareImpl, ok := spare.(*classifier)
	require.True(t, ok)
	assert.Nil(t, spareImpl.req)
	assert.Nil(t, spareImpl.compiled)
	assert.Nil(t, spareImpl.inBuf)

	tripErr := gpuFault.trip(errors.NewStd("simulated CL_OUT_OF_RESOURCES"))
	require.ErrorIs(t, tripErr, ErrGPUDeviceFault)

	_, err = gpuClassifier.PredictRaw(silence)
	require.ErrorIs(t, err, ErrGPUDeviceFault, "GPU inference fails fast once latched")

	_, err = NewClassifier(model, Options{PrecisionHint: DefaultPrecisionHint, Device: DeviceGPU})
	require.ErrorIs(t, err, ErrGPUDeviceFault, "a GPU compile is refused once latched")

	cpuClassifier, err := NewClassifier(model, Options{PrecisionHint: DefaultPrecisionHint, Device: DeviceCPU})
	require.NoError(t, err, "the CPU device keeps working after a GPU trip")
	out, err := cpuClassifier.PredictRaw(silence)
	require.NoError(t, err)
	assert.Len(t, out, birdNETOutputClasses)
	require.NoError(t, cpuClassifier.Close())

	require.NoError(t, gpuClassifier.Close(), "Close abandons the GPU objects and returns nil")
	gpuImpl, ok := gpuClassifier.(*classifier)
	require.True(t, ok)
	assert.Nil(t, gpuImpl.req, "an abandoned classifier holds no native handle")
	assert.Nil(t, gpuImpl.compiled)
	assert.Nil(t, gpuImpl.inBuf)

	require.NoError(t, DestroyOV(), "DestroyOV abandons the core and returns nil")
	ovMu.Lock()
	assert.Nil(t, ovCore)
	assert.False(t, ovLoaded)
	ovMu.Unlock()
}
