package onnx

import (
	"math"
	"slices"
)

func sigmoid(x float32) float32 {
	return 1.0 / (1.0 + float32(math.Exp(float64(-x))))
}

func sigmoidSlice(logits []float32) []float32 {
	result := make([]float32, len(logits))
	for i, v := range logits {
		result[i] = sigmoid(v)
	}
	return result
}

func softmax(logits []float32) []float32 {
	if len(logits) == 0 {
		// Return a fresh empty slice (never the input) so callers relying on the
		// "newly allocated, never aliases logits" contract (activationFor) hold even
		// for an unexpected empty output, whose backing tensor the caller destroys.
		return []float32{}
	}
	result := make([]float32, len(logits))
	maxVal := logits[0]
	for _, v := range logits[1:] {
		if v > maxVal {
			maxVal = v
		}
	}
	var sum float32
	for i, v := range logits {
		result[i] = float32(math.Exp(float64(v - maxVal)))
		sum += result[i]
	}
	for i := range result {
		result[i] /= sum
	}
	return result
}

// activationFor converts a model's raw output tensor into per-class scores.
// BirdNET v2.4 applies sigmoid and Perch v2 applies softmax in post-processing,
// while BirdNET v3.0 applies its per-class sigmoid in-graph: its "predictions"
// output is already probabilities in [0,1], so it is passed through unchanged
// (re-applying sigmoid would double-squash the scores). The returned slice is
// always newly allocated and never aliases logits, because the caller destroys
// the backing ONNX output tensor after post-processing.
func activationFor(mt ModelType, logits []float32) []float32 {
	switch mt {
	case BirdNETv24:
		return sigmoidSlice(logits)
	case BirdNETv30:
		return slices.Clone(logits)
	case PerchV2:
		return softmax(logits)
	}
	// Defensive fallback for an unhandled model type. ModelType is a closed
	// three-value enum, all handled above, so this is unreachable in practice.
	return sigmoidSlice(logits)
}

func topK(scores []float32, labels []string, k int, minConf float32) []Prediction {
	var preds []Prediction
	n := min(len(scores), len(labels))
	for i := range n {
		if scores[i] >= minConf {
			preds = append(preds, Prediction{
				Species:    labels[i],
				Confidence: scores[i],
				Index:      i,
			})
		}
	}
	slices.SortFunc(preds, func(a, b Prediction) int {
		if a.Confidence > b.Confidence {
			return -1
		}
		if a.Confidence < b.Confidence {
			return 1
		}
		return 0
	})
	if k > 0 && len(preds) > k {
		preds = preds[:k]
	}
	return preds
}
