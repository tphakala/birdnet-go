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
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
)

const (
	// v24ParityDefaultMaxConfDiff is the largest allowed confidence change between
	// the OpenVINO candidate and the ONNX Runtime reference for any label.
	v24ParityDefaultMaxConfDiff = 0.02
	// v24ParityMinTop1Confidence is the reference top-1 confidence below which the
	// top-1 label is not compared: near-silent windows have many labels tied near
	// zero, so their order is noise rather than a result.
	v24ParityMinTop1Confidence = 0.1
	// v24ParityLocale, v24ParitySensitivity and v24ParityThreads are the fixture
	// settings both classifiers load with: English labels, the neutral sigmoid
	// sensitivity, and one thread so the two runs are comparable.
	v24ParityLocale      = "en"
	v24ParitySensitivity = 1.0
	v24ParityThreads     = 1
)

// TestBirdNETV24OpenVINO_LowFrequencyParity drives the real NewBirdNET load path
// for a BirdNET v2.4 FP32 model twice, once on the openvino backend (the OpenVINO
// plan an opted-in host takes, device and precision included) and once on ONNX Runtime as the
// reference, and compares their scores window by window. It is the regression
// check for GitHub #4986: on the A76 CPU, OpenVINO's default f16 zeroes the scores
// of windows whose power is mostly below about 150 Hz (wind, handling noise), so
// the candidate must compile at FP32 (openVINOPrecisionFor) and keep every
// window's top-1 and scores within the tolerance of ONNX Runtime.
//
// Model-gated so normal CI stays green (CI builds and vets the openvino tag but
// never runs these tests). Skipped unless OV_V24_MODEL and OV_V24_WINDOWS are set.
// On an ARMv8.2+ CPU with native f16, or with OV_V24_DEVICE set, it requires the
// OpenVINO backend, so a missing libopenvino_c or a model file without the fp32
// token fails the test there instead of skipping it. Env knobs:
//
//   - OV_V24_MODEL:   path to a BirdNET v2.4 FP32 ONNX model whose file name carries
//     the fp32 token (for example BirdNET_v2.4_fp32_dfttrunc.onnx), so the openvino
//     backend runs it at f32.
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

	candidate := newV24ForParity(t, modelPath, conf.BackendPrefOpenVINO)
	device, backend, precision := candidate.RuntimeInfo()
	t.Logf("candidate backend=%s device=%s precision=%s", backend, device, precision)
	if backend != BackendOpenVINO {
		// the openvino backend plans OpenVINO for an fp32-token v2.4 file on an
		// ARMv8.2+ CPU with native f16 (and on an Intel GPU, which only an explicit
		// device can demand here), so a fallback is the regression on such a host.
		dev := os.Getenv("OV_V24_DEVICE")
		if dev == "" && !cpuspec.HasNativeF16() {
			t.Skipf("OpenVINO backend not active on this host (backend=%s); set OV_V24_DEVICE to require it", backend)
		}
		require.Equalf(t, BackendOpenVINO, backend,
			"BirdNET v2.4 fell back to %s (OV_V24_DEVICE=%q, native f16 CPU=%t): libopenvino_c is not loadable, the model file lacks the fp32 token, or the OpenVINO path regressed",
			backend, dev, cpuspec.HasNativeF16())
	}
	require.Equal(t, string(QuantizationFP32), precision,
		"BirdNET v2.4 on OpenVINO must run at FP32 (openVINOPrecisionFor), not the f16 default; see GitHub #4986")

	reference := newV24ForParity(t, modelPath, conf.BackendPrefONNX)
	_, refBackend, _ := reference.RuntimeInfo()
	require.Equal(t, BackendONNX, refBackend, "the reference must run on ONNX Runtime")

	windows := len(samples) / windowSamples
	var worst float32
	labels := candidate.currentSettings().BirdNET.Labels
	for w := range windows {
		window := samples[w*windowSamples : (w+1)*windowSamples]
		ref := v24ParityScores(t, reference, window)
		got := v24ParityScores(t, candidate, window)
		require.Len(t, got, len(ref), "both backends must score every label")
		require.Len(t, labels, len(ref), "one label per score")

		diff, at := maxScoreDiff(ref, got)
		worst = max(worst, diff)
		assert.LessOrEqualf(t, diff, maxDiff, "window %d: %s moved by %.4f (reference %.4f, candidate %.4f)",
			w, labels[at], diff, ref[at], got[at])
		// The candidate's top-1 must be a reference top-1 within the score limit, so
		// two labels tied for first place do not fail the comparison.
		refTop, gotTop := argmaxScore(ref), argmaxScore(got)
		if ref[refTop] >= v24ParityMinTop1Confidence {
			assert.LessOrEqualf(t, ref[refTop]-ref[gotTop], maxDiff, "window %d: top-1 changed from %s (%.4f) to %s, which the reference scores %.4f",
				w, labels[refTop], ref[refTop], labels[gotTop], ref[gotTop])
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
	settings.BirdNET.Locale = v24ParityLocale
	settings.BirdNET.Sensitivity = v24ParitySensitivity
	settings.BirdNET.Threads = v24ParityThreads
	settings.BirdNET.Backend = backendPref
	settings.BirdNET.OpenVINODevice = os.Getenv("OV_V24_DEVICE")
	settings.BirdNET.OpenVINOPath = os.Getenv("OV_V24_LIB")
	settings.BirdNET.ONNXRuntimePath = os.Getenv("OV_V24_ORT_LIB")

	bn, err := NewBirdNET(settings, nil, nil)
	require.NoError(t, err)
	t.Cleanup(bn.Delete)
	return bn
}

// v24ParityScores returns the sensitivity-adjusted confidence of every label for
// one window from bn's inference backend, the full vector Predict ranks before it
// keeps the top results, so the comparison covers labels below that cutoff too.
func v24ParityScores(t *testing.T, bn *BirdNET, window []float32) []float32 {
	t.Helper()
	logits, err := bn.classifier.Predict(window)
	require.NoError(t, err)
	require.NotEmpty(t, logits)
	// Reject non-finite logits the way BirdNET.Predict does: a NaN score would
	// compare false against the limit and pass unnoticed.
	idx := firstNonFinite(logits)
	require.Equalf(t, noNonFiniteScore, idx, "non-finite logit at index %d of %d", idx, len(logits))
	return applySigmoidToPredictions(logits, v24ParitySensitivity)
}

// maxScoreDiff returns the largest absolute difference between two equal-length
// score vectors and the index where it occurs.
func maxScoreDiff(ref, got []float32) (diff float32, at int) {
	for i := range ref {
		if d := float32(math.Abs(float64(ref[i] - got[i]))); d > diff {
			diff, at = d, i
		}
	}
	return diff, at
}

// argmaxScore returns the index of the highest score.
func argmaxScore(scores []float32) int {
	best := 0
	for i, v := range scores {
		if v > scores[best] {
			best = i
		}
	}
	return best
}
