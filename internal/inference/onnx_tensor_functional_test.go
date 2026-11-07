//go:build onnx

package inference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewONNXTensorModel_Functional runs the adapter against a real model. It
// is skipped unless ONNX_TEST_TENSOR_MODEL points at a single-input ONNX model.
// ONNX_TEST_ORT_LIB optionally names the libonnxruntime path.
func TestNewONNXTensorModel_Functional(t *testing.T) {
	modelPath := os.Getenv("ONNX_TEST_TENSOR_MODEL")
	if modelPath == "" {
		t.Skip("set ONNX_TEST_TENSOR_MODEL to a single-input ONNX model to run")
	}
	require.NoError(t, InitONNXRuntime(os.Getenv("ONNX_TEST_ORT_LIB")))

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
	require.Len(t, outputs, len(m.OutputSizes()))
	for i, o := range outputs {
		assert.Len(t, o, m.OutputSizes()[i])
	}
}
