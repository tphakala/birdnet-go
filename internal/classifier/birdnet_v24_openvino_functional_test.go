//go:build openvino

package classifier

import (
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
)

const (
	// v24ParityDefaultMaxConfDiff is the largest allowed confidence change between
	// the OpenVINO candidate and the ONNX Runtime reference for any label in either
	// top-K list.
	v24ParityDefaultMaxConfDiff = 0.02
	// v24ParityMinTop1Confidence is the reference top-1 confidence below which the
	// top-1 label is not compared: near-silent windows have many labels tied near
	// zero, so their order is noise rather than a result.
	v24ParityMinTop1Confidence = 0.1
)

// TestBirdNETV24OpenVINO_LowFrequencyParity drives the real NewBirdNET load path
// for a BirdNET v2.4 FP32 model twice, once on the auto backend (the production
// OpenVINO plan, device and precision included) and once on ONNX Runtime as the
// reference, and compares their scores window by window. It is the regression
// check for GitHub #4986: on the A76 CPU, OpenVINO's default f16 zeroes the scores
// of windows whose power is mostly below about 150 Hz (wind, handling noise), so
// the candidate must compile at FP32 (openVINOPrecisionFor) and keep every
// window's top-1 and scores within the tolerance of ONNX Runtime.
//
// Hardware/lib/model-gated so normal CI stays green (CI never builds the openvino
// tag). Skipped unless OV_V24_MODEL and OV_V24_WINDOWS are set. Env knobs:
//
//   - OV_V24_MODEL:   path to a BirdNET v2.4 FP32 ONNX model whose file name carries
//     the fp32 token (for example BirdNET_v2.4_fp32_dfttrunc.onnx), so auto plans
//     OpenVINO for it.
//   - OV_V24_WINDOWS: raw little-endian float32 PCM, a whole number of 144000-sample
//     (48 kHz, 3 s) windows. Use low-frequency-heavy windows; clean ones pass at f16.
//   - OV_V24_LABELS:  optional label file (embedded English labels when unset).
//   - OV_V24_DEVICE:  "", "cpu" or "gpu" (BirdNET.OpenVINODevice; "" = auto, which
//     probes for a GPU through openvino_probe_child_test.go).
//   - OV_V24_LIB / OV_V24_ORT_LIB: optional libopenvino_c / libonnxruntime paths.
//   - OV_V24_MAX_CONF_DIFF: allowed confidence change (default 0.02).
func TestBirdNETV24OpenVINO_LowFrequencyParity(t *testing.T) {
	modelPath := os.Getenv("OV_V24_MODEL")
	windowsPath := os.Getenv("OV_V24_WINDOWS")
	if modelPath == "" || windowsPath == "" {
		t.Skip("set OV_V24_MODEL and OV_V24_WINDOWS to run the BirdNET v2.4 OpenVINO parity test")
	}

	maxDiff := float32(v24ParityDefaultMaxConfDiff)
	if v := os.Getenv("OV_V24_MAX_CONF_DIFF"); v != "" {
		parsed, err := strconv.ParseFloat(v, 32)
		require.NoError(t, err)
		maxDiff = float32(parsed)
	}

	// Validate the windows before the two model loads, so a bad file fails fast.
	windowSamples := onnx.BirdNETv24.SampleCount()
	samples := readV3AudioOrZeros(t, windowsPath)
	require.NotEmpty(t, samples)
	require.Zero(t, len(samples)%windowSamples, "windows file must hold whole %d-sample windows", windowSamples)

	candidate := newV24ForParity(t, modelPath, conf.BackendPrefAuto)
	device, backend, precision := candidate.RuntimeInfo()
	t.Logf("candidate backend=%s device=%s precision=%s", backend, device, precision)
	if backend != BackendOpenVINO {
		if dev := os.Getenv("OV_V24_DEVICE"); dev != "" {
			t.Fatalf("OV_V24_DEVICE=%s requested but BirdNET v2.4 fell back to %s; the OpenVINO path regressed", dev, backend)
		}
		t.Skipf("OpenVINO backend not active on this host (backend=%s); set OV_V24_DEVICE to require it", backend)
	}
	require.Equal(t, string(QuantizationFP32), precision,
		"BirdNET v2.4 on OpenVINO must run at FP32 (openVINOPrecisionFor), not the f16 default; see GitHub #4986")

	reference := newV24ForParity(t, modelPath, conf.BackendPrefONNX)
	_, refBackend, _ := reference.RuntimeInfo()
	require.Equal(t, BackendONNX, refBackend, "the reference must run on ONNX Runtime")

	windows := len(samples) / windowSamples
	var worst float32
	for w := range windows {
		window := samples[w*windowSamples : (w+1)*windowSamples]
		ref, err := reference.Predict(t.Context(), [][]float32{window})
		require.NoError(t, err)
		got, err := candidate.Predict(t.Context(), [][]float32{window})
		require.NoError(t, err)
		require.NotEmpty(t, ref)
		require.NotEmpty(t, got)

		diff := topKConfidenceDiff(ref, got)
		worst = max(worst, diff)
		assert.LessOrEqualf(t, diff, maxDiff, "window %d: confidence moved by %.4f (reference top-1 %s %.4f, candidate top-1 %s %.4f)",
			w, diff, ref[0].Species, ref[0].Confidence, got[0].Species, got[0].Confidence)
		if ref[0].Confidence >= v24ParityMinTop1Confidence {
			assert.Equalf(t, ref[0].Species, got[0].Species, "window %d: top-1 changed", w)
		}
	}
	t.Logf("%d windows, worst confidence change %.6f (limit %.4f)", windows, worst, maxDiff)
}

// newV24ForParity builds a BirdNET v2.4 classifier for modelPath on the given
// backend preference through NewBirdNET, so the candidate takes the same OpenVINO
// plan the realtime pipeline would.
func newV24ForParity(t *testing.T, modelPath, backendPref string) *BirdNET {
	t.Helper()
	settings := &conf.Settings{}
	settings.BirdNET.ModelPath = modelPath
	settings.BirdNET.LabelPath = os.Getenv("OV_V24_LABELS")
	settings.BirdNET.Locale = "en"
	settings.BirdNET.Sensitivity = 1.0
	settings.BirdNET.Threads = 1
	settings.BirdNET.Backend = backendPref
	settings.BirdNET.OpenVINODevice = os.Getenv("OV_V24_DEVICE")
	settings.BirdNET.OpenVINOPath = os.Getenv("OV_V24_LIB")
	settings.BirdNET.ONNXRuntimePath = os.Getenv("OV_V24_ORT_LIB")

	bn, err := NewBirdNET(settings, nil, nil)
	require.NoError(t, err)
	t.Cleanup(bn.Delete)
	return bn
}

// topKConfidenceDiff returns the largest confidence change between two top-K
// result lists over every label in either list. A label missing from one list
// scored at most that list's lowest confidence, so the lowest confidence stands
// in for it.
func topKConfidenceDiff(ref, got []datastore.Results) float32 {
	refByLabel, refMin := confidenceByLabel(ref)
	gotByLabel, gotMin := confidenceByLabel(got)
	var worst float32
	for label, r := range refByLabel {
		g, ok := gotByLabel[label]
		if !ok {
			g = gotMin
		}
		worst = max(worst, float32(math.Abs(float64(r-g))))
	}
	for label, g := range gotByLabel {
		if _, ok := refByLabel[label]; !ok {
			worst = max(worst, float32(math.Abs(float64(g-refMin))))
		}
	}
	return worst
}

// confidenceByLabel indexes results by label and returns the lowest confidence.
func confidenceByLabel(results []datastore.Results) (byLabel map[string]float32, lowest float32) {
	byLabel = make(map[string]float32, len(results))
	lowest = results[0].Confidence
	for _, r := range results {
		byLabel[r.Species] = r.Confidence
		lowest = min(lowest, r.Confidence)
	}
	return byLabel, lowest
}
