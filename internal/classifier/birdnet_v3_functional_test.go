//go:build onnx

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

// birdnetV3InputSampleCount is the BirdNET v3.0 input length (32 kHz * 5 s).
const birdnetV3InputSampleCount = 160000

// TestBirdNETV3_Functional is a model-gated end-to-end test that loads a real
// BirdNET v3.0 ONNX model and runs one inference through the full BirdNETV3.Predict
// path (ONNX Runtime backend). It is skipped unless V3_TEST_MODEL and V3_TEST_LABELS
// point at a real GPU-native v3.0 model and its "SciName_CommonName" label file, so
// normal CI stays green.
//
// Optional env:
//   - V3_TEST_LIB:   libonnxruntime.so path (default /usr/lib/libonnxruntime.so).
//   - V3_TEST_AUDIO: raw little-endian float32, mono, 32 kHz PCM. When set, a 5 s
//     window is fed to the model and the top detections are logged; otherwise a
//     silent (zero) buffer is used to exercise the plumbing only.
//
// It proves the production path end to end: label parsing, ORT load, the
// size-based predictions-port detection, and the no-activation output handling
// (the model's in-graph sigmoid is not re-applied), returning one score per label
// in [0,1].
func TestBirdNETV3_Functional(t *testing.T) {
	model := os.Getenv("V3_TEST_MODEL")
	labelPath := os.Getenv("V3_TEST_LABELS")
	if model == "" || labelPath == "" {
		t.Skip("set V3_TEST_MODEL and V3_TEST_LABELS to run the BirdNET v3.0 functional test")
	}

	labelData, err := os.ReadFile(labelPath)
	require.NoError(t, err)
	labels, err := ParseBirdNETV3Labels(labelData)
	require.NoError(t, err)
	require.NotEmpty(t, labels)

	lib := os.Getenv("V3_TEST_LIB")
	if lib == "" {
		lib = "/usr/lib/libonnxruntime.so"
	}

	// Predict records to the global metrics; publish a test registry so the span
	// bookkeeping has somewhere to write.
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	SetMetrics(newTestMetrics(t))

	cfg := BirdNETV3Config{
		ModelPath:       model,
		LabelPath:       labelPath,
		ONNXRuntimePath: lib,
		Threads:         4,
		Backend:         conf.BackendPrefONNX, // force the ORT path for a deterministic CI-less run
	}

	m, err := NewBirdNETV3(&cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	require.Equal(t, len(labels), m.NumSpecies(), "NumSpecies must equal the label count")
	require.Equal(t, 32000, m.Spec().SampleRate)

	samples := loadV3Audio(t, birdnetV3InputSampleCount)

	results, err := m.Predict(t.Context(), [][]float32{samples})
	require.NoError(t, err)
	require.NotEmpty(t, results, "inference must return at least one detection")

	// The model applies its per-class sigmoid in-graph, so every score is a
	// probability in [0,1]; results are sorted by confidence descending.
	for i := range results {
		assert.GreaterOrEqual(t, results[i].Confidence, float32(0), "confidence must be >= 0")
		assert.LessOrEqual(t, results[i].Confidence, float32(1), "confidence must be <= 1 (no double sigmoid)")
		if i > 0 {
			assert.LessOrEqual(t, results[i].Confidence, results[i-1].Confidence, "results must be sorted descending")
		}
	}

	for i := range results {
		t.Logf("v3.0 top %d: %-45s %.4f", i+1, results[i].Species, results[i].Confidence)
	}
}

// loadV3Audio returns a mono 32 kHz float32 window of length n. If V3_TEST_AUDIO
// points at a raw little-endian float32 file it is used (truncated or zero-padded
// to n); otherwise a silent buffer is returned.
func loadV3Audio(t *testing.T, n int) []float32 {
	t.Helper()
	samples := make([]float32, n)

	path := os.Getenv("V3_TEST_AUDIO")
	if path == "" {
		return samples
	}

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	avail := len(raw) / 4
	count := min(avail, n)
	for i := range count {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return samples
}
