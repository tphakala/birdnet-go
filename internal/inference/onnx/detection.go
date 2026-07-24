package onnx

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// Known model sample counts and output counts for auto-detection.
const (
	sampleCountV24   = 144000 // BirdNET v2.4: 48kHz * 3s
	sampleCountV30   = 160000 // BirdNET v3.0 / Perch v2: 32kHz * 5s
	numOutputsV24    = 1      // BirdNET v2.4: logits only
	numOutputsV24Emb = 2      // BirdNET v2.4 with embeddings: logits + embeddings
	numOutputsV30    = 2      // BirdNET v3.0: embeddings + logits
	numOutputsPerch  = 4      // Perch v2: embeddings + features + attention + logits

	embeddingSizeV24   = 1024 // BirdNET v2.4 embedding dimension (head-pruned embedding-only model)
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
	case sampleCount == sampleCountV24 && numOutputs == numOutputsV24Emb:
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

func buildModelConfig(mt ModelType, inputShape []int64, outputShapes [][]int64) ModelConfig {
	numOutputs := len(outputShapes)
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
		if numOutputs == 1 && lastDim(outputShapes[0]) == embeddingSizeV24 {
			// Head-pruned embedding-only backbone: a single [.,1024] embedding output
			// (the [6522] logits head is pruned). Distinguished from the plain
			// 1-output classifier ([.,6522] logits) by output size. LogitsSize stays 0
			// and LogitsIndex is -1 to mark that this model produces no logits.
			//
			// Disambiguation is by output size: a hypothetical custom BirdNET v2.4
			// classifier with exactly 1024 output classes would be misread as
			// embedding-only. The shipped models make this safe (the classifier head is
			// 6522 classes, the backbone embedding is 1024), and a 1024-class v2.4
			// classifier is not a configuration this project produces.
			cfg.LogitsIndex = -1
			cfg.EmbeddingIndex = 0
			cfg.EmbeddingSize = embeddingSizeV24
		} else {
			cfg.LogitsIndex = 0
			cfg.LogitsSize = lastDim(outputShapes[0])
			if numOutputs >= 2 {
				if embSize := lastDim(outputShapes[1]); embSize > 0 {
					cfg.EmbeddingIndex = 1
					cfg.EmbeddingSize = embSize
				}
			}
		}
	case BirdNETv30:
		cfg.LogitsIndex = 1
		cfg.LogitsSize = lastDim(outputShapes[1])
		cfg.EmbeddingIndex = 0
		cfg.EmbeddingSize = embeddingSizeV30
	case PerchV2:
		cfg.LogitsIndex = 3
		cfg.LogitsSize = lastDim(outputShapes[3])
		cfg.EmbeddingIndex = 0
		cfg.EmbeddingSize = embeddingSizePerch
	}

	return cfg
}

func lastDim(shape []int64) int {
	if len(shape) == 0 {
		return 0
	}
	return int(shape[len(shape)-1])
}

// DetectEmbeddingOutput inspects an ONNX model's output tensors and returns the
// index and size of its BirdNET v2.4 embedding output (the [.,1024] tensor). The
// bat OpenVINO path uses this to bind the extractor to the correct output port up
// front, before inference. Returns a ModelDetectionError when the model has no
// 1024-dim output.
func DetectEmbeddingOutput(modelPath string) (index, size int, err error) {
	_, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return -1, 0, fmt.Errorf("birdnet: failed to load model metadata: %w", err)
	}
	return selectEmbeddingOutput(outputInfos)
}

// selectEmbeddingOutput returns the index and size of the BirdNET v2.4 embedding
// output ([.,1024]) among a model's output tensors: index 1 for the 2-output backbone
// (logits@0 + embedding@1), index 0 for the head-pruned, embedding-only model. Returns
// a ModelDetectionError when no 1024-dim output exists. Split out so the port-selection
// logic is unit-testable without a real ONNX model.
func selectEmbeddingOutput(outputInfos []ort.InputOutputInfo) (index, size int, err error) {
	for i := range outputInfos {
		if lastDim(outputInfos[i].Dimensions) == embeddingSizeV24 {
			return i, embeddingSizeV24, nil
		}
	}
	return -1, 0, &ModelDetectionError{
		Reason: fmt.Sprintf("model has no %d-dim embedding output", embeddingSizeV24),
	}
}
