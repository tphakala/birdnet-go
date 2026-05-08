//go:build !onnx

package classifier

import "fmt"

// Bat is unavailable without ONNX support.
type Bat struct{}

// BatModelConfig holds configuration for creating a Bat model instance.
type BatModelConfig struct {
	EmbeddingModelPath  string
	EmbeddingLabels     []string
	ClassifierModelPath string
	ClassifierLabelPath string
	ONNXRuntimePath     string
	Threads             int
}

// NewBat returns an error when built without ONNX support.
func NewBat(_ *BatModelConfig) (*Bat, error) {
	return nil, fmt.Errorf("Bat model requires ONNX support (build with -tags onnx)")
}
