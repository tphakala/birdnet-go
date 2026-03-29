//go:build !onnx

package classifier

import "fmt"

// Perch is unavailable without ONNX support.
type Perch struct{}

// PerchConfig holds configuration for creating a Perch model instance.
type PerchConfig struct {
	ModelPath       string
	LabelPath       string
	ONNXRuntimePath string
	Threads         int
}

// NewPerch returns an error when built without ONNX support.
func NewPerch(_ PerchConfig) (*Perch, error) {
	return nil, fmt.Errorf("Perch model requires ONNX support (build with -tags onnx)")
}
