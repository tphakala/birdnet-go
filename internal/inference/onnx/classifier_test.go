package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildModelConfig_V24WithEmbeddings(t *testing.T) {
	t.Parallel()
	cfg := buildModelConfig(BirdNETv24, []int64{1, 144000}, [][]int64{{1, 6522}, {1, 1024}})
	assert.Equal(t, 1, cfg.EmbeddingIndex)
	assert.Equal(t, 1024, cfg.EmbeddingSize)
}

func TestBuildModelConfig_V24NoEmbeddings(t *testing.T) {
	t.Parallel()
	cfg := buildModelConfig(BirdNETv24, []int64{1, 144000}, [][]int64{{1, 6522}})
	assert.Equal(t, -1, cfg.EmbeddingIndex)
	assert.Equal(t, 0, cfg.EmbeddingSize)
}

func TestClassifier_EmbeddingDim(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1024, (&Classifier{config: ModelConfig{EmbeddingSize: 1024}}).EmbeddingDim())
	assert.Equal(t, 0, (&Classifier{config: ModelConfig{EmbeddingSize: 0}}).EmbeddingDim())
}
