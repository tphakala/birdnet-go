//go:build openvino

package classifier

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBirdNETV3OpenVINO_ForcesF32AndStaysFinite drives the real NewBirdNETV3
// dispatch on the OpenVINO backend and asserts the BIRDNET-GO-2H6 fix end to end:
// the classifier compiles at FP32 (openVINOPrecisionFor forces it for v3.0 on every
// device, so RuntimeInfo reports FP32, not the f16 default) and Predict returns only
// finite, in-range scores. Before the fix the OV path ran at f16, which overflows to
// NaN on fp16-weight regional tiles and inflates scores ~25-30x on fp32-weight tiles.
//
// Hardware/lib/model-gated so normal CI stays green (CI never builds the openvino
// tag). Skipped unless OV_V3_MODEL and OV_V3_LABELS are set. Env knobs:
//
//   - OV_V3_MODEL:   path to a BirdNET v3.0 ONNX tile (fp16 or fp32 weights).
//   - OV_V3_LABELS:  matching labels file, one "Scientific_Common" per line.
//   - OV_V3_AUDIO:   optional raw little-endian float32 PCM (32 kHz mono, 5 s =
//     160000 samples). Zeros if unset, but a real clip exercises realistic activations.
//   - OV_V3_DEVICE:  "", "cpu", or "gpu" (BirdNET.OpenVINODevice; "" = auto).
//   - OV_V3_LIB / OV_V3_ORT_LIB: optional libopenvino_c / libonnxruntime paths.
func TestBirdNETV3OpenVINO_ForcesF32AndStaysFinite(t *testing.T) {
	modelPath := os.Getenv("OV_V3_MODEL")
	labelPath := os.Getenv("OV_V3_LABELS")
	if modelPath == "" || labelPath == "" {
		t.Skip("set OV_V3_MODEL and OV_V3_LABELS to run the BirdNET v3.0 OpenVINO functional test")
	}

	cfg := BirdNETV3Config{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: os.Getenv("OV_V3_ORT_LIB"),
		Threads:         1,
		Backend:         conf.BackendPrefOpenVINO,
		OpenVINOPath:    os.Getenv("OV_V3_LIB"),
		OpenVINODevice:  os.Getenv("OV_V3_DEVICE"),
	}

	model, err := NewBirdNETV3(&cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, model.Close()) })

	device, backend, precision := model.RuntimeInfo()
	t.Logf("backend=%s device=%s precision=%s species=%d", backend, device, precision, model.NumSpecies())
	if backend != BackendOpenVINO {
		t.Skipf("OpenVINO backend not active on this host (backend=%s); cannot exercise the f16->f32 fix", backend)
	}

	// The fix: v3.0 on OpenVINO must compile at FP32 on every device, so the f16
	// overflow/inflation cannot occur. A regression that reverted the forcing would
	// report FP16 here.
	require.Equal(t, string(QuantizationFP32), precision,
		"BirdNET v3.0 on OpenVINO must run at FP32 (openVINOPrecisionFor), not the f16 default; see BIRDNET-GO-2H6")

	samples := readV3AudioOrZeros(t, os.Getenv("OV_V3_AUDIO"))
	results, err := model.Predict(t.Context(), [][]float32{samples})
	// Predict itself rejects non-finite scores (firstNonFinite) and the in-graph
	// sigmoid bounds finite scores to [0,1], so require.NoError here IS the finiteness
	// guard: at FP32 the window must not fault the way f16 does (BIRDNET-GO-2H6).
	require.NoError(t, err, "Predict must not return a non-finite-score error at FP32")
	require.NotEmpty(t, results)

	// Assert the aggregate is a real, non-degenerate probability, so an all-zero or
	// collapsed output would still fail here rather than pass silently.
	var maxConf float32
	for i := range results {
		if c := results[i].Confidence; c > maxConf {
			maxConf = c
		}
	}
	require.Positive(t, maxConf, "top confidence must be > 0 (non-degenerate output)")
	require.LessOrEqual(t, maxConf, float32(1), "post-sigmoid confidence must be <= 1")
	t.Logf("top confidence=%.4f across %d results", maxConf, len(results))
}

// readV3AudioOrZeros reads a raw little-endian float32 PCM file, or returns a silent
// BirdNET-v3.0-sized buffer (32 kHz * 5 s) when no path is given.
func readV3AudioOrZeros(t *testing.T, path string) []float32 {
	t.Helper()
	const birdnetV3Samples = 160000 // 32 kHz * 5 s
	if path == "" {
		return make([]float32, birdnetV3Samples)
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Zero(t, len(data)%4, "audio file must be whole float32 samples")
	out := make([]float32, len(data)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return out
}
