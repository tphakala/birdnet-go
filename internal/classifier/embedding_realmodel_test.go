package classifier

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Environment variables that gate the real-model embedding integration test.
// All three must be set for the test to run; otherwise it skips. CI has no
// model file, so the default path is always a clean skip.
const (
	// envRealModelPath holds the path to a real ONNX BirdNET model. The .onnx
	// extension is what selects the ONNX backend, which is the only backend
	// that exposes embeddings.
	envRealModelPath = "BIRDNET_EMBEDDING_TEST_MODEL"
	// envRealModelLabels holds the path to the model's labels file.
	envRealModelLabels = "BIRDNET_EMBEDDING_TEST_LABELS"
	// envRealModelDim holds the expected embedding dimension (e.g. 1024 for the
	// v2.4 embedding model).
	envRealModelDim = "BIRDNET_EMBEDDING_TEST_DIM"
)

// defaultSensitivity is the sigmoid sensitivity used for the integration run. It
// only affects post-processing of detection scores, not the embedding vector,
// so any valid value works; 1.0 is the neutral midpoint.
const defaultSensitivity = 1.0

// TestRealModel_EmbeddingExtraction loads a real ONNX BirdNET model from disk and
// verifies that a single forward pass produces a non-nil embedding of the
// expected dimension. It is skipped unless BIRDNET_EMBEDDING_TEST_MODEL,
// BIRDNET_EMBEDDING_TEST_LABELS, and BIRDNET_EMBEDDING_TEST_DIM are all set, so
// it never runs in CI (no model file is shipped there).
func TestRealModel_EmbeddingExtraction(t *testing.T) {
	modelPath := os.Getenv(envRealModelPath)
	labelPath := os.Getenv(envRealModelLabels)
	dimStr := os.Getenv(envRealModelDim)
	if modelPath == "" || labelPath == "" || dimStr == "" {
		t.Skipf("set %s, %s, %s to run (e.g. the 66MB v2.4-emb .onnx, dim 1024)",
			envRealModelPath, envRealModelLabels, envRealModelDim)
	}

	wantDim, err := strconv.Atoi(dimStr)
	require.NoError(t, err, "%s must be an integer", envRealModelDim)

	settings := &conf.Settings{}
	settings.BirdNET.ModelPath = modelPath
	settings.BirdNET.LabelPath = labelPath
	settings.BirdNET.Sensitivity = defaultSensitivity

	// The .onnx extension on ModelPath routes NewBirdNET to the ONNX backend,
	// which is the primary that exposes embeddings.
	bn, err := NewBirdNET(settings, &ModelInfo{ID: "realmodel-test"})
	require.NoError(t, err, "loading the real ONNX model should succeed")
	t.Cleanup(bn.Delete)

	require.Equal(t, wantDim, bn.EmbeddingDim(), "model should expose the expected embedding dim")

	// The ONNX backend requires exactly one clip worth of samples. Derive the
	// count from the loaded model's spec (sample rate times clip length in
	// seconds) rather than hardcoding a window size. A clip of silence is enough
	// to exercise the forward pass.
	spec := bn.Spec()
	sampleCount := spec.SampleRate * int(spec.ClipLength.Seconds())
	require.Positive(t, sampleCount, "model spec must yield a positive sample count")
	sample := make([]float32, sampleCount)

	_, emb, err := bn.PredictWithEmbeddings(t.Context(), [][]float32{sample})
	require.NoError(t, err, "embedding-capable forward pass should succeed")
	require.NotNil(t, emb, "embedding-capable model must return a non-nil embedding")
	assert.Len(t, emb, wantDim, "embedding length should match the expected dim")
}
