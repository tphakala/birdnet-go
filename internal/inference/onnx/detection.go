//go:build onnx

package onnx

import "fmt"

// Known model sample counts and output counts for auto-detection.
const (
	sampleCountV24  = 144000 // BirdNET v2.4: 48kHz * 3s
	sampleCountV30  = 160000 // BirdNET v3.0 / Perch v2: 32kHz * 5s
	numOutputsV24   = 1      // BirdNET v2.4: logits only
	numOutputsV30   = 2      // BirdNET v3.0: embeddings + logits
	numOutputsPerch = 4      // Perch v2: embeddings + features + attention + logits

	embeddingSizeV30   = 1280 // BirdNET v3.0 embedding dimension
	embeddingSizePerch = 1536 // Perch v2 embedding dimension
)

// detectModelTypeFromShapes determines the ModelType from input tensor shapes and output count.
func detectModelTypeFromShapes(inputShapes [][]int64, numOutputs int) (ModelType, error) {
	if len(inputShapes) == 0 {
		return 0, &ModelDetectionError{Reason: "model has no input tensors"}
	}

	shape := inputShapes[0]
	if len(shape) < 2 {
		return 0, &ModelDetectionError{Reason: fmt.Sprintf("input shape has %d dimensions, expected at least 2", len(shape))}
	}

	sampleCount := shape[len(shape)-1]

	switch {
	case sampleCount == sampleCountV24 && numOutputs == numOutputsV24:
		return BirdNETv24, nil
	case sampleCount == sampleCountV30 && numOutputs == numOutputsV30:
		return BirdNETv30, nil
	case sampleCount == sampleCountV30 && numOutputs == numOutputsPerch:
		return PerchV2, nil
	default:
		return 0, &ModelDetectionError{
			Reason: fmt.Sprintf("unrecognized model: %d input samples, %d outputs", sampleCount, numOutputs),
		}
	}
}

func buildModelConfig(mt ModelType, inputShape []int64, numOutputs int) ModelConfig {
	cfg := ModelConfig{
		Type:           mt,
		SampleRate:     mt.SampleRate(),
		Duration:       mt.Duration(),
		SampleCount:    mt.SampleCount(),
		NumOutputs:     numOutputs,
		EmbeddingIndex: -1,
		InputShape:     make([]int64, len(inputShape)),
	}
	copy(cfg.InputShape, inputShape)

	switch mt {
	case BirdNETv24:
		cfg.LogitsIndex = 0
		cfg.EmbeddingSize = 0
	case BirdNETv30:
		cfg.LogitsIndex = 1
		cfg.EmbeddingIndex = 0
		cfg.EmbeddingSize = embeddingSizeV30
	case PerchV2:
		cfg.LogitsIndex = 3
		cfg.EmbeddingIndex = 0
		cfg.EmbeddingSize = embeddingSizePerch
	}

	return cfg
}
