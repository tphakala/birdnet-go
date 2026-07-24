package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

// v24LogitsSize is the BirdNET v2.4 species-logits dimension, used here to build
// representative output shapes for the model-config tests.
const v24LogitsSize = 6522

// TestBuildModelConfig_BirdNETv24 covers the three BirdNET v2.4 output layouts the
// bat pipeline must distinguish: the current 2-output backbone (logits@0 +
// embedding@1), the head-pruned embedding-only model (embedding@0, no logits), and
// the plain single-output classifier (logits@0, no embedding). The embedding-only
// case is disambiguated from the plain classifier purely by output size (1024 vs
// 6522), since both have a single output.
func TestBuildModelConfig_BirdNETv24(t *testing.T) {
	t.Parallel()
	inputShape := []int64{1, sampleCountV24}
	tests := []struct {
		name           string
		outputShapes   [][]int64
		wantLogitsIdx  int
		wantLogitsSize int
		wantEmbIdx     int
		wantEmbSize    int
	}{
		{
			name:           "two-output backbone (logits@0 + embedding@1)",
			outputShapes:   [][]int64{{1, v24LogitsSize}, {1, embeddingSizeV24}},
			wantLogitsIdx:  0,
			wantLogitsSize: v24LogitsSize,
			wantEmbIdx:     1,
			wantEmbSize:    embeddingSizeV24,
		},
		{
			name:           "head-pruned embedding-only (embedding@0, no logits)",
			outputShapes:   [][]int64{{1, embeddingSizeV24}},
			wantLogitsIdx:  -1,
			wantLogitsSize: 0,
			wantEmbIdx:     0,
			wantEmbSize:    embeddingSizeV24,
		},
		{
			name:           "plain single-output classifier (logits@0, no embedding)",
			outputShapes:   [][]int64{{1, v24LogitsSize}},
			wantLogitsIdx:  0,
			wantLogitsSize: v24LogitsSize,
			wantEmbIdx:     -1,
			wantEmbSize:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := buildModelConfig(BirdNETv24, inputShape, tt.outputShapes)
			assert.Equal(t, BirdNETv24, cfg.Type)
			assert.Equal(t, len(tt.outputShapes), cfg.NumOutputs)
			assert.Equal(t, tt.wantLogitsIdx, cfg.LogitsIndex, "LogitsIndex")
			assert.Equal(t, tt.wantLogitsSize, cfg.LogitsSize, "LogitsSize")
			assert.Equal(t, tt.wantEmbIdx, cfg.EmbeddingIndex, "EmbeddingIndex")
			assert.Equal(t, tt.wantEmbSize, cfg.EmbeddingSize, "EmbeddingSize")
		})
	}
}

// TestValidateLabelCount checks that an embedding-only model (LogitsIndex < 0) skips
// label validation entirely (it has no logits to validate against), while a normal
// logits model still validates the label count and rejects an out-of-range index.
func TestValidateLabelCount(t *testing.T) {
	t.Parallel()

	t.Run("embedding-only model without skip is rejected", func(t *testing.T) {
		t.Parallel()
		cfg := &ModelConfig{LogitsIndex: -1}
		// An embedding-only model has no logits to match labels against, so with
		// validation enabled it must fail fast rather than error only at prediction
		// time. The bat pipeline loads such models with SkipLabelValidation, which
		// bypasses this function entirely.
		require.Error(t, validateLabelCount(cfg, nil, 999))
	})

	t.Run("matching label count passes", func(t *testing.T) {
		t.Parallel()
		cfg := &ModelConfig{LogitsIndex: 0}
		outputs := []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v24LogitsSize)}}
		require.NoError(t, validateLabelCount(cfg, outputs, v24LogitsSize))
	})

	t.Run("mismatched label count errors", func(t *testing.T) {
		t.Parallel()
		cfg := &ModelConfig{LogitsIndex: 0}
		outputs := []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v24LogitsSize)}}
		require.Error(t, validateLabelCount(cfg, outputs, v24LogitsSize-1))
	})

	t.Run("logits index out of range errors", func(t *testing.T) {
		t.Parallel()
		cfg := &ModelConfig{LogitsIndex: 2}
		outputs := []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v24LogitsSize)}}
		require.Error(t, validateLabelCount(cfg, outputs, v24LogitsSize))
	})
}

// TestOutputShape_BirdNETv24 verifies createOutputTensors gets correctly-sized output
// shapes for each v2.4 layout. The critical case is the head-pruned model, whose only
// output (index 0) is the [.,1024] embedding rather than logits: a wrong size here
// would allocate a zero- or mis-sized tensor and fail inference.
func TestOutputShape_BirdNETv24(t *testing.T) {
	t.Parallel()

	t.Run("embedding-only model sizes output 0 as the embedding", func(t *testing.T) {
		t.Parallel()
		c := &Classifier{config: ModelConfig{
			Type: BirdNETv24, NumOutputs: 1,
			LogitsIndex: -1, LogitsSize: 0,
			EmbeddingIndex: 0, EmbeddingSize: embeddingSizeV24,
		}}
		shape, err := c.outputShape(0, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, embeddingSizeV24}, shape)
	})

	t.Run("two-output model sizes logits@0 and embedding@1", func(t *testing.T) {
		t.Parallel()
		c := &Classifier{config: ModelConfig{
			Type: BirdNETv24, NumOutputs: 2,
			LogitsIndex: 0, LogitsSize: v24LogitsSize,
			EmbeddingIndex: 1, EmbeddingSize: embeddingSizeV24,
		}}
		logits, err := c.outputShape(0, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, v24LogitsSize}, logits)
		emb, err := c.outputShape(1, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, embeddingSizeV24}, emb)
	})

	t.Run("single-output classifier sizes logits@0", func(t *testing.T) {
		t.Parallel()
		c := &Classifier{config: ModelConfig{
			Type: BirdNETv24, NumOutputs: 1,
			LogitsIndex: 0, LogitsSize: v24LogitsSize,
			EmbeddingIndex: -1,
		}}
		shape, err := c.outputShape(0, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, v24LogitsSize}, shape)
	})
}

// TestSelectEmbeddingOutput covers the port-selection logic behind DetectEmbeddingOutput
// without a real ONNX model: the [.,1024] embedding is at index 1 in the 2-output
// backbone and at index 0 in the head-pruned model, and a model with no 1024-dim output
// is an error.
func TestSelectEmbeddingOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		outputs   []ort.InputOutputInfo
		wantIndex int
		wantSize  int
		wantErr   bool
	}{
		{
			name:      "two-output backbone: embedding at index 1",
			outputs:   []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v24LogitsSize)}, {Dimensions: ort.NewShape(1, embeddingSizeV24)}},
			wantIndex: 1,
			wantSize:  embeddingSizeV24,
		},
		{
			name:      "head-pruned: embedding at index 0",
			outputs:   []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, embeddingSizeV24)}},
			wantIndex: 0,
			wantSize:  embeddingSizeV24,
		},
		{
			name:    "no 1024-dim output errors",
			outputs: []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v24LogitsSize)}},
			wantErr: true,
		},
		{
			name:    "nil outputs errors",
			outputs: nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx, size, err := selectEmbeddingOutput(tt.outputs)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, -1, idx)
				assert.Equal(t, 0, size)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIndex, idx)
			assert.Equal(t, tt.wantSize, size)
		})
	}
}

// v30LogitsSize is the BirdNET v3.0 species-predictions dimension.
const v30LogitsSize = 11560

// TestBuildModelConfig_BirdNETv30 verifies the v3.0 ports are resolved by output
// size, not position: the GPU-native export orders outputs embeddings@0 +
// predictions@1, while the stock export uses the reverse. Both must load with the
// embeddings port bound to the 1280-dim output and predictions to the other.
func TestBuildModelConfig_BirdNETv30(t *testing.T) {
	t.Parallel()
	inputShape := []int64{1, sampleCountV30}
	tests := []struct {
		name          string
		outputShapes  [][]int64
		wantLogitsIdx int
		wantEmbIdx    int
	}{
		{
			name:          "gpu-native order (embeddings@0, predictions@1)",
			outputShapes:  [][]int64{{1, embeddingSizeV30}, {1, v30LogitsSize}},
			wantLogitsIdx: 1,
			wantEmbIdx:    0,
		},
		{
			name:          "stock order (predictions@0, embeddings@1)",
			outputShapes:  [][]int64{{1, v30LogitsSize}, {1, embeddingSizeV30}},
			wantLogitsIdx: 0,
			wantEmbIdx:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := buildModelConfig(BirdNETv30, inputShape, tt.outputShapes)
			assert.Equal(t, BirdNETv30, cfg.Type)
			assert.Equal(t, tt.wantLogitsIdx, cfg.LogitsIndex, "LogitsIndex")
			assert.Equal(t, v30LogitsSize, cfg.LogitsSize, "LogitsSize")
			assert.Equal(t, tt.wantEmbIdx, cfg.EmbeddingIndex, "EmbeddingIndex")
			assert.Equal(t, embeddingSizeV30, cfg.EmbeddingSize, "EmbeddingSize")
		})
	}
}

// TestOutputShape_BirdNETv30 verifies createOutputTensors sizes each v3.0 output
// correctly for both export orderings (index-aware, driven by the resolved ports).
func TestOutputShape_BirdNETv30(t *testing.T) {
	t.Parallel()
	inputShape := []int64{1, sampleCountV30}
	orders := [][][]int64{
		{{1, embeddingSizeV30}, {1, v30LogitsSize}},
		{{1, v30LogitsSize}, {1, embeddingSizeV30}},
	}
	for _, outputShapes := range orders {
		cfg := buildModelConfig(BirdNETv30, inputShape, outputShapes)
		c := &Classifier{config: cfg}
		emb, err := c.outputShape(cfg.EmbeddingIndex, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, embeddingSizeV30}, emb)
		logits, err := c.outputShape(cfg.LogitsIndex, 1)
		require.NoError(t, err)
		assert.Equal(t, []int64{1, v30LogitsSize}, logits)
	}
}

// TestSelectPredictionsOutput covers the port-selection logic behind
// DetectPredictionsOutput without a real ONNX model: the predictions output is the
// one matching the label count, at either position, and a model with no matching
// output is an error.
func TestSelectPredictionsOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		outputs    []ort.InputOutputInfo
		numClasses int
		wantIndex  int
		wantErr    bool
	}{
		{
			name:       "predictions at index 1 (embeddings-first)",
			outputs:    []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, embeddingSizeV30)}, {Dimensions: ort.NewShape(1, v30LogitsSize)}},
			numClasses: v30LogitsSize,
			wantIndex:  1,
		},
		{
			name:       "predictions at index 0 (predictions-first)",
			outputs:    []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, v30LogitsSize)}, {Dimensions: ort.NewShape(1, embeddingSizeV30)}},
			numClasses: v30LogitsSize,
			wantIndex:  0,
		},
		{
			name:       "no matching output errors",
			outputs:    []ort.InputOutputInfo{{Dimensions: ort.NewShape(1, embeddingSizeV30)}},
			numClasses: v30LogitsSize,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			idx, err := selectPredictionsOutput(tt.outputs, tt.numClasses)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, -1, idx)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIndex, idx)
		})
	}
}

// TestProcessOutput_EmbeddingOnlyModelErrors confirms that calling the logits-based
// Predict path on an embedding-only model returns a clear error instead of panicking
// on outputs[-1]. The guard returns before indexing, so nil outputs is sufficient.
func TestProcessOutput_EmbeddingOnlyModelErrors(t *testing.T) {
	t.Parallel()
	c := &Classifier{config: ModelConfig{
		Type: BirdNETv24, NumOutputs: 1,
		LogitsIndex: -1, EmbeddingIndex: 0, EmbeddingSize: embeddingSizeV24,
	}}
	res, err := c.processOutput(nil, 0)
	require.Error(t, err)
	assert.Nil(t, res)
}
