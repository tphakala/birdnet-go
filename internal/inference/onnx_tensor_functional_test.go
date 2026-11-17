//go:build onnx

package inference

import (
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// envTensorModel names the single-input ONNX model the test loads.
	envTensorModel = "ONNX_TEST_TENSOR_MODEL"
	// envTensorLib optionally names the libonnxruntime path.
	envTensorLib = "ONNX_TEST_ORT_LIB"
)

// TestNewONNXTensorModel_Functional runs the adapter against a real model. It
// is skipped unless ONNX_TEST_TENSOR_MODEL points at a single-input ONNX model.
// ONNX_TEST_ORT_LIB optionally names the libonnxruntime path.
func TestNewONNXTensorModel_Functional(t *testing.T) {
	modelPath := os.Getenv(envTensorModel)
	if modelPath == "" {
		t.Skipf("set %s to a single-input ONNX model to run", envTensorModel)
	}
	require.NoError(t, InitONNXRuntime(os.Getenv(envTensorLib)))

	m, err := NewONNXTensorModel(modelPath, ONNXTensorModelOptions{Threads: 1})
	require.NoError(t, err)
	t.Cleanup(m.Close)

	shape := m.InputShape()
	require.NotEmpty(t, shape)
	assert.Equal(t, int64(1), shape[0])

	count := int64(1)
	for _, d := range shape {
		count *= d
	}
	outputs, err := m.Run(make([]float32, count))
	require.NoError(t, err)
	sizes := m.OutputSizes()
	require.Len(t, outputs, len(sizes))
	for i, o := range outputs {
		assert.Len(t, o, sizes[i])
		for j, v := range o {
			require.Falsef(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "output %d value %d is not finite: %v", i, j, v)
		}
	}
}
