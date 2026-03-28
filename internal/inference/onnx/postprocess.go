//go:build onnx

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
		return logits
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
