//go:build onnx

package onnx

import (
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	envTensorModel = "ONNX_TEST_TENSOR_MODEL"
	envTensorLib   = "ONNX_TEST_ORT_LIB"

	// tensorTestClassCount is the output width of the model the test is
	// written against (22 classes, input [batch, 1, 512, 128]).
	tensorTestClassCount = 22
)

// TestTensorModel_Functional loads a real single-input ONNX model with an N-D
// float input and runs it on zeros. It is skipped unless ONNX_TEST_TENSOR_MODEL
// points at the model, so normal CI stays green. Env:
//
//   - ONNX_TEST_TENSOR_MODEL: path to the ONNX model (input [batch, 1, 512, 128],
//     one output of 22 values).
//   - ONNX_TEST_ORT_LIB: optional libonnxruntime path (empty = default discovery).
//
// It exercises the N-D tensor path against real ONNX Runtime.
func TestTensorModel_Functional(t *testing.T) {
	modelPath := os.Getenv(envTensorModel)
	if modelPath == "" {
		t.Skipf("set %s to an ONNX model with one N-D float input to run", envTensorModel)
	}

	if !ort.IsInitialized() {
		MustInitORT(os.Getenv(envTensorLib))
	}

	m, err := NewTensorModel(modelPath, TensorModelOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, m.Close()) })

	assert.Equal(t, []int64{1, 1, 512, 128}, m.InputShape(), "a dynamic batch dimension must be pinned to 1")
	assert.Equal(t, []int{tensorTestClassCount}, m.OutputSizes())

	const inputElements = 1 * 1 * 512 * 128 // matches the asserted input shape
	outputs, err := m.Run(make([]float32, inputElements))
	require.NoError(t, err)
	require.Len(t, outputs, 1)
	require.Len(t, outputs[0], tensorTestClassCount)
	for i, v := range outputs[0] {
		require.Falsef(t, math.IsNaN(float64(v)) || math.IsInf(float64(v), 0), "output %d is not finite: %v", i, v)
	}

	_, err = m.Run(make([]float32, inputElements+1))
	var sizeErr *TensorInputSizeError
	require.ErrorAs(t, err, &sizeErr)

	require.NoError(t, m.Close())
	_, err = m.Run(make([]float32, inputElements))
	require.ErrorIs(t, err, ErrSessionClosed)
}
