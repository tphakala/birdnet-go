package onnx

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

// TestClassifierEmbeddingOnly_Functional loads a real head-pruned, 1-output BirdNET
// v2.4 embedding model and exercises the inference paths the bat pipeline depends on.
// It is the ORT-side companion to TestOpenVINOBatEmbeddingParity_Functional and is
// skipped unless ONNX_TEST_EMB_ONLY_MODEL points at such a model, so normal CI stays
// green (the pruned model is not vendored). Env:
//
//   - ONNX_TEST_EMB_ONLY_MODEL: path to the head-pruned embedding-only ONNX model
//     ([batch,144000] -> single [.,1024] embedding output).
//   - ONNX_TEST_ORT_LIB:        optional libonnxruntime path (empty = default discovery).
func TestClassifierEmbeddingOnly_Functional(t *testing.T) {
	modelPath := os.Getenv("ONNX_TEST_EMB_ONLY_MODEL")
	if modelPath == "" {
		t.Skip("set ONNX_TEST_EMB_ONLY_MODEL to a head-pruned 1-output embedding ONNX model to run")
	}

	// Idempotent guard: other gated tests in this package may already have initialized
	// the runtime. Do not DestroyORT here for the same reason.
	if !ort.IsInitialized() {
		MustInitORT(os.Getenv("ONNX_TEST_ORT_LIB"))
	}

	// Mirror how the bat pipeline loads the embedding model: a placeholder label with
	// validation skipped, since the model produces no species logits to match.
	c, err := NewClassifier(modelPath,
		WithLabels([]string{"placeholder"}),
		WithSkipLabelValidation(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, c.Close()) })

	cfg := c.Config()
	assert.Equal(t, BirdNETv24, cfg.Type)
	assert.Equal(t, 1, cfg.NumOutputs)
	assert.Equal(t, -1, cfg.LogitsIndex, "embedding-only model must report no logits")
	assert.Equal(t, 0, cfg.EmbeddingIndex)
	assert.Equal(t, embeddingSizeV24, cfg.EmbeddingSize)

	samples := make([]float32, cfg.SampleCount)

	// e1: the exact path the bat pipeline uses. It must return nil logits and a full
	// embedding vector without panicking on outputs[LogitsIndex] at index -1.
	logits, emb, err := c.PredictRawWithEmbeddings(samples)
	require.NoError(t, err)
	assert.Nil(t, logits, "embedding-only model must return nil logits")
	assert.Len(t, emb, embeddingSizeV24)

	// e2: the logits-based prediction paths must fail cleanly (an error, never a panic)
	// on a model that has no logits output.
	_, err = c.Predict(samples)
	require.Error(t, err, "Predict must error on an embedding-only model")

	_, err = c.PredictBatch([][]float32{samples})
	require.Error(t, err, "PredictBatch must error on an embedding-only model")
}
