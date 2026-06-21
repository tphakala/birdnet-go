//go:build openvino

package inference

import (
	"bufio"
	"encoding/binary"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestOpenVINOParity_Functional is a hardware/lib/model-gated parity test that
// runs the SAME ONNX model through both the ONNX Runtime backend (the reference)
// and the OpenVINO backend, on identical input, and compares their outputs. It is
// the evidence harness behind enabling BirdNET v2.4 on the Intel iGPU (the GPU
// fence in classifier.openVINOPlanFor): it measures how far OpenVINO f16 drifts
// from ORT f32 for the BirdNET v2.4 sigmoid model, the way Perch was validated.
//
// It is skipped unless OV_TEST_PARITY_MODEL and OV_TEST_PARITY_LABELS are set, so
// normal CI stays green. Env knobs:
//
//   - OV_TEST_PARITY_MODEL:   path to the FP32 ONNX model (e.g. birdnet-v24.onnx).
//   - OV_TEST_PARITY_LABELS:  labels file, one per line; its count must equal the
//     model output dimension.
//   - OV_TEST_PARITY_AUDIO:   optional raw little-endian float32 PCM file of input
//     samples (e.g. 48 kHz mono, 3 s = 144000 samples for BirdNET v2.4). Zeros if
//     unset, but a real clip gives a meaningful top-k and a realistic f16 error.
//   - OV_TEST_DEVICE:         "cpu", "gpu", or "auto"/"" (defaults to "GPU" here,
//     since the iGPU is what the fence removal is about). Mapped to OV device.
//   - OV_TEST_PRECISION:      "", "f16", or "f32" INFERENCE_PRECISION_HINT. Empty
//     uses the backend default (f16). Use "f32" for the GPU-precision experiment.
//   - OV_TEST_LIB / OV_TEST_ORT_LIB: optional libopenvino_c / libonnxruntime paths.
//   - OV_TEST_MAX_CONF_ERR:   max allowed |sigmoid(ov)-sigmoid(ort)| (default 0.05).
func TestOpenVINOParity_Functional(t *testing.T) {
	modelPath := os.Getenv("OV_TEST_PARITY_MODEL")
	labelPath := os.Getenv("OV_TEST_PARITY_LABELS")
	if modelPath == "" || labelPath == "" {
		t.Skip("set OV_TEST_PARITY_MODEL and OV_TEST_PARITY_LABELS to run the OpenVINO parity test")
	}

	labels := readLabels(t, labelPath)
	require.NotEmpty(t, labels)

	samples := readAudioOrZeros(t, os.Getenv("OV_TEST_PARITY_AUDIO"))

	device := mapDevice(os.Getenv("OV_TEST_DEVICE"))
	precision := os.Getenv("OV_TEST_PRECISION")

	maxConfErr := 0.05
	if v := os.Getenv("OV_TEST_MAX_CONF_ERR"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		require.NoError(t, err)
		maxConfErr = parsed
	}

	// Reference backend: ONNX Runtime (f32).
	require.NoError(t, InitONNXRuntime(os.Getenv("OV_TEST_ORT_LIB")))
	t.Cleanup(func() { _ = DestroyONNXRuntime() })
	ort, err := NewONNXClassifier(modelPath, ONNXClassifierOptions{Labels: labels, Threads: 1})
	require.NoError(t, err)
	t.Cleanup(ort.Close)

	// Candidate backend: OpenVINO on the selected device/precision.
	require.NoError(t, InitOpenVINO(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOpenVINO() })
	ov, err := NewOpenVINOClassifier(modelPath, OpenVINOClassifierOptions{
		Labels:        labels,
		Threads:       1,
		Device:        device,
		PrecisionHint: precision,
	})
	require.NoError(t, err)
	t.Cleanup(ov.Close)

	require.Equal(t, len(labels), ort.NumSpecies())
	require.Equal(t, len(labels), ov.NumSpecies(), "#1112: OV NumSpecies must equal real model output dim")

	ortLogits, err := ort.Predict(samples)
	require.NoError(t, err)
	ovLogits, err := ov.Predict(samples)
	require.NoError(t, err)
	require.Len(t, ortLogits, len(labels))
	require.Len(t, ovLogits, len(labels))

	// Element-wise error in logits and in post-sigmoid confidence.
	var maxLogitErr, maxConfDiff float64
	for i := range ortLogits {
		if d := math.Abs(float64(ovLogits[i] - ortLogits[i])); d > maxLogitErr {
			maxLogitErr = d
		}
		if d := math.Abs(sigmoid(ovLogits[i]) - sigmoid(ortLogits[i])); d > maxConfDiff {
			maxConfDiff = d
		}
	}

	ortTop := topK(ortLogits, 5)
	ovTop := topK(ovLogits, 5)

	t.Logf("device=%s precision=%q model=%s", device, precision, modelPath)
	t.Logf("max |logit_ov - logit_ort| = %.6g", maxLogitErr)
	t.Logf("max |conf_ov  - conf_ort | = %.6g (sigmoid)", maxConfDiff)
	t.Logf("ORT top-5: %s", fmtTop(ortTop, ortLogits, labels))
	t.Logf("OV  top-5: %s", fmtTop(ovTop, ovLogits, labels))

	require.Equal(t, ortTop[0], ovTop[0],
		"top-1 species must match between ORT and OpenVINO (ORT=%q OV=%q)",
		labels[ortTop[0]], labels[ovTop[0]])
	require.LessOrEqual(t, maxConfDiff, maxConfErr,
		"OpenVINO confidence drift from ORT exceeds tolerance")

	// Optional latency comparison: only a GPU win over ORT CPU justifies routing
	// BirdNET to the iGPU. Set OV_TEST_PARITY_ITERS to time both backends.
	if iters := os.Getenv("OV_TEST_PARITY_ITERS"); iters != "" {
		n, err := strconv.Atoi(iters)
		require.NoError(t, err)
		require.Positive(t, n, "OV_TEST_PARITY_ITERS must be > 0")
		t.Logf("ORT median: %s/seg", medianPredict(t, ort, samples, n))
		t.Logf("OV  median: %s/seg", medianPredict(t, ov, samples, n))
	}
}

// medianPredict times n inferences (after 3 warmup runs) and returns the median.
func medianPredict(t *testing.T, c Classifier, samples []float32, n int) time.Duration {
	t.Helper()
	for range 3 {
		_, err := c.Predict(samples)
		require.NoError(t, err)
	}
	ds := make([]time.Duration, n)
	for i := range n {
		start := time.Now()
		_, err := c.Predict(samples)
		require.NoError(t, err)
		ds[i] = time.Since(start)
	}
	sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
	return ds[n/2]
}

// TestOpenVINOPrecisionDivergence_Functional compares the SAME model on the same
// OpenVINO device at f16 vs f32, with no ORT reference. It is the decisive check
// for "does this device's f16 kernel break this model on realistic input" without
// needing the ORT output-port conventions to line up, so it works for multi-output
// models like Perch v2 (logits at OV_TEST_DIVERGE_OUTPUT_INDEX, default 0).
//
// Skipped unless OV_TEST_DIVERGE_MODEL and OV_TEST_DIVERGE_LABELS are set. Other
// env: OV_TEST_DIVERGE_AUDIO (raw f32 input), OV_TEST_DIVERGE_OUTPUT_INDEX,
// OV_TEST_DEVICE (default "gpu"), OV_TEST_LIB, OV_TEST_DIVERGE_MAX_CONF_ERR
// (default 0.15: cross-precision drift is inherently larger than ORT-vs-f32, e.g.
// Perch v2 f16-GPU is ~0.08 while a broken model like BirdNET v2.4 f16-GPU is
// ~0.8; the top-1 match assertion is the primary catch for a catastrophic
// divergence). The divergence knob is named distinctly from the parity test's
// OV_TEST_MAX_CONF_ERR so the two tolerances cannot be conflated.
func TestOpenVINOPrecisionDivergence_Functional(t *testing.T) {
	modelPath := os.Getenv("OV_TEST_DIVERGE_MODEL")
	labelPath := os.Getenv("OV_TEST_DIVERGE_LABELS")
	if modelPath == "" || labelPath == "" {
		t.Skip("set OV_TEST_DIVERGE_MODEL and OV_TEST_DIVERGE_LABELS to run the OpenVINO precision divergence test")
	}

	labels := readLabels(t, labelPath)
	samples := readAudioOrZeros(t, os.Getenv("OV_TEST_DIVERGE_AUDIO"))
	device := mapDevice(os.Getenv("OV_TEST_DEVICE"))

	outputIndex := 0
	if v := os.Getenv("OV_TEST_DIVERGE_OUTPUT_INDEX"); v != "" {
		parsed, err := strconv.Atoi(v)
		require.NoError(t, err)
		outputIndex = parsed
	}
	maxConfErr := 0.15
	if v := os.Getenv("OV_TEST_DIVERGE_MAX_CONF_ERR"); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		require.NoError(t, err)
		maxConfErr = parsed
	}

	require.NoError(t, InitOpenVINO(os.Getenv("OV_TEST_LIB")))
	t.Cleanup(func() { _ = DestroyOpenVINO() })

	build := func(precision string) Classifier {
		c, err := NewOpenVINOClassifier(modelPath, OpenVINOClassifierOptions{
			Labels:        labels,
			Threads:       1,
			Device:        device,
			OutputIndex:   outputIndex,
			PrecisionHint: precision,
		})
		require.NoError(t, err)
		t.Cleanup(c.Close)
		return c
	}

	f32, f16 := build("f32"), build("f16")
	f32Logits, err := f32.Predict(samples)
	require.NoError(t, err)
	f16Logits, err := f16.Predict(samples)
	require.NoError(t, err)

	var maxLogitErr, maxConfDiff float64
	for i := range f32Logits {
		if d := math.Abs(float64(f16Logits[i] - f32Logits[i])); d > maxLogitErr {
			maxLogitErr = d
		}
		if d := math.Abs(sigmoid(f16Logits[i]) - sigmoid(f32Logits[i])); d > maxConfDiff {
			maxConfDiff = d
		}
	}
	f32Top, f16Top := topK(f32Logits, 5), topK(f16Logits, 5)
	t.Logf("device=%s outputIndex=%d model=%s", device, outputIndex, modelPath)
	t.Logf("max |logit_f16 - logit_f32| = %.6g", maxLogitErr)
	t.Logf("max |conf_f16  - conf_f32 | = %.6g (sigmoid)", maxConfDiff)
	t.Logf("f32 top-5: %s", fmtTop(f32Top, f32Logits, labels))
	t.Logf("f16 top-5: %s", fmtTop(f16Top, f16Logits, labels))

	require.Equal(t, f32Top[0], f16Top[0],
		"f16 top-1 must match f32 top-1 (f32=%q f16=%q)", labels[f32Top[0]], labels[f16Top[0]])
	require.LessOrEqual(t, maxConfDiff, maxConfErr, "f16 confidence drift from f32 exceeds tolerance")
}

func sigmoid(x float32) float64 { return 1.0 / (1.0 + math.Exp(-float64(x))) }

// mapDevice maps an OV_TEST_DEVICE value to an OpenVINO device string, defaulting
// to the GPU since the iGPU path is what these parity runs exist to validate.
func mapDevice(v string) string {
	switch strings.ToLower(v) {
	case "cpu":
		return OVDeviceCPU
	case "", "gpu", "auto":
		return OVDeviceGPU
	default:
		return v
	}
}

func readLabels(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var labels []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			labels = append(labels, line)
		}
	}
	require.NoError(t, sc.Err())
	return labels
}

// readAudioOrZeros reads a raw little-endian float32 PCM file, or returns a
// silent BirdNET-v2.4-sized buffer when no path is given.
func readAudioOrZeros(t *testing.T, path string) []float32 {
	t.Helper()
	const birdnetV24Samples = 144000 // 48 kHz * 3 s
	if path == "" {
		return make([]float32, birdnetV24Samples)
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

func topK(logits []float32, k int) []int {
	idx := make([]int, len(logits))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return logits[idx[a]] > logits[idx[b]] })
	if k > len(idx) {
		k = len(idx)
	}
	return idx[:k]
}

func fmtTop(top []int, logits []float32, labels []string) string {
	var b strings.Builder
	for n, i := range top {
		if n > 0 {
			b.WriteString(", ")
		}
		b.WriteString(labels[i])
		b.WriteString("=")
		b.WriteString(strconv.FormatFloat(sigmoid(logits[i]), 'f', 4, 64))
	}
	return b.String()
}
