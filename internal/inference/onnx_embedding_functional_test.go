//go:build onnx

package inference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// envBatEmbModel names the BirdNET v2.4 embedding backbone the bat pipeline loads.
	envBatEmbModel = "ONNX_TEST_BAT_EMB_MODEL"
	// batEmbSamples is one 3 s window at 48 kHz, the backbone's input length.
	batEmbSamples = 3 * 48000
	// batEmbDim is the backbone's embedding width.
	batEmbDim = 1024
)

// TestNewONNXClassifier_LabelFreeEmbedding_Functional loads the bat embedding
// backbone the way NewBat does on the ONNX Runtime path, with no labels, and checks
// that it yields an embedding and reports its real logits width. It is skipped
// unless ONNX_TEST_BAT_EMB_MODEL points at the model; ONNX_TEST_ORT_LIB optionally
// names the libonnxruntime path.
func TestNewONNXClassifier_LabelFreeEmbedding_Functional(t *testing.T) {
	modelPath := os.Getenv(envBatEmbModel)
	if modelPath == "" {
		t.Skipf("set %s to the bat embedding model to run", envBatEmbModel)
	}
	require.NoError(t, InitONNXRuntime(os.Getenv(envTensorLib)))

	c, err := NewONNXClassifier(modelPath, ONNXClassifierOptions{Threads: 1, SkipLabelValidation: true})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	ext, ok := c.(EmbeddingExtractor)
	require.True(t, ok, "the ONNX classifier must implement EmbeddingExtractor")

	logits, emb, err := ext.PredictWithEmbeddings(make([]float32, batEmbSamples))
	require.NoError(t, err)
	assert.Len(t, emb, batEmbDim)
	assert.Len(t, logits, c.NumSpecies(), "NumSpecies must report the model's logits width, not the empty label count")
}
