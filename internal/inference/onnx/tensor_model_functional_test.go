//go:build onnx

package onnx

import (
	"math"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	envTensorModel = "ONNX_TEST_TENSOR_MODEL"
	envTensorLib   = "ONNX_TEST_ORT_LIB"

	// envTensorMultiOutputModel names an optional model with several outputs,
	// used to check output selection.
	envTensorMultiOutputModel = "ONNX_TEST_TENSOR_MULTI_OUTPUT_MODEL"

	// closeRaceRunners is the number of goroutines running the model while it is closed.
	closeRaceRunners = 8

	// tensorTestTolerance bounds the difference between two runs of one model on one input.
	tensorTestTolerance = 1e-5

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
//   - ONNX_TEST_TENSOR_MULTI_OUTPUT_MODEL: optional path to a model with at least
//     two outputs of different sizes, used only by the output selection test.
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

func initTensorTestORT(t *testing.T) {
	t.Helper()
	if !ort.IsInitialized() {
		MustInitORT(os.Getenv(envTensorLib))
	}
}

// TestTensorModel_Functional_RejectsBadOutputIndex pins that an output index
// outside the model's outputs fails construction.
func TestTensorModel_Functional_RejectsBadOutputIndex(t *testing.T) {
	modelPath := os.Getenv(envTensorModel)
	if modelPath == "" {
		t.Skipf("set %s to an ONNX model with one N-D float input to run", envTensorModel)
	}
	initTensorTestORT(t)

	_, allOutputs, err := ort.GetInputOutputInfo(modelPath)
	require.NoError(t, err)

	for _, idx := range []int{-1, len(allOutputs)} {
		_, err := NewTensorModel(modelPath, TensorModelOptions{Outputs: []int{idx}})
		var detErr *ModelDetectionError
		require.ErrorAsf(t, err, &detErr, "output index %d must be rejected", idx)
		assert.Contains(t, detErr.Reason, "out of range")
	}
}

// TestTensorModel_Functional_OutputSelection pins that Outputs binds only the
// chosen outputs, in the given order. It needs a model with at least two
// outputs and is skipped unless ONNX_TEST_TENSOR_MULTI_OUTPUT_MODEL names one.
func TestTensorModel_Functional_OutputSelection(t *testing.T) {
	modelPath := os.Getenv(envTensorMultiOutputModel)
	if modelPath == "" {
		t.Skipf("set %s to an ONNX model with at least two outputs to run", envTensorMultiOutputModel)
	}
	initTensorTestORT(t)

	all, err := NewTensorModel(modelPath, TensorModelOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, all.Close()) })
	allSizes := all.OutputSizes()
	require.GreaterOrEqual(t, len(allSizes), 2)

	last := len(allSizes) - 1
	require.NotEqual(t, allSizes[last], allSizes[0], "the model's first and last outputs must differ in size to tell their order apart")
	selected, err := NewTensorModel(modelPath, TensorModelOptions{Outputs: []int{last, 0}})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, selected.Close()) })
	assert.Equal(t, []int{allSizes[last], allSizes[0]}, selected.OutputSizes())

	count := 1
	for _, d := range selected.InputShape() {
		count *= int(d)
	}
	input := make([]float32, count)
	for i := range input {
		input[i] = float32(i%7) / 7
	}
	wantAll, err := all.Run(input)
	require.NoError(t, err)
	got, err := selected.Run(input)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.InDeltaSlice(t, wantAll[last], got[0], tensorTestTolerance)
	assert.InDeltaSlice(t, wantAll[0], got[1], tensorTestTolerance)
}

// TestTensorModel_Functional_CloseDuringRun pins that Close during concurrent
// Run calls ends every runner with ErrSessionClosed and nothing else.
func TestTensorModel_Functional_CloseDuringRun(t *testing.T) {
	modelPath := os.Getenv(envTensorModel)
	if modelPath == "" {
		t.Skipf("set %s to an ONNX model with one N-D float input to run", envTensorModel)
	}
	initTensorTestORT(t)

	m, err := NewTensorModel(modelPath, TensorModelOptions{})
	require.NoError(t, err)

	count := 1
	for _, d := range m.InputShape() {
		count *= int(d)
	}
	input := make([]float32, count)

	// Every runner loops until the session reports it is closed, and Close
	// waits for the first successful run, so runs are in flight when it fires.
	firstRun := make(chan struct{})
	var firstRunOnce sync.Once
	errs := make([]error, closeRaceRunners)
	var wg sync.WaitGroup
	for i := range closeRaceRunners {
		wg.Go(func() {
			for {
				if _, err := m.Run(input); err != nil {
					errs[i] = err
					return
				}
				firstRunOnce.Do(func() { close(firstRun) })
			}
		})
	}
	<-firstRun
	require.NoError(t, m.Close())
	wg.Wait()

	for i, err := range errs {
		assert.ErrorIsf(t, err, ErrSessionClosed, "runner %d", i)
	}
}
