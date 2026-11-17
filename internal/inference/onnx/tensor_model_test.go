package onnx

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ort "github.com/yalue/onnxruntime_go"
)

func TestResolveTensorShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		dims      []int64
		wantShape []int64
		wantCount int
		wantErr   string
	}{
		{name: "dynamic batch pinned to one", dims: []int64{-1, 1, 512, 128}, wantShape: []int64{1, 1, 512, 128}, wantCount: 65536},
		{name: "fixed batch kept", dims: []int64{1, 2, 96, 511}, wantShape: []int64{1, 2, 96, 511}, wantCount: 2 * 96 * 511},
		{name: "fixed batch above one kept", dims: []int64{4, 8}, wantShape: []int64{4, 8}, wantCount: 32},
		{name: "dynamic inner dimension rejected", dims: []int64{1, -1, 128}, wantErr: "dimension 1"},
		{name: "dynamic trailing dimension rejected", dims: []int64{-1, 3, -1}, wantErr: "dimension 2"},
		{name: "zero dimension rejected", dims: []int64{-1, 0, 4}, wantErr: "dimension 1"},
		{name: "empty shape rejected", dims: nil, wantErr: "no dimensions"},
		{name: "element count overflow rejected", dims: []int64{1, math.MaxInt32, math.MaxInt32, math.MaxInt32}, wantErr: "too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			shape, count, err := resolveTensorShape(tt.dims)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				var detErr *ModelDetectionError
				assert.ErrorAs(t, err, &detErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantShape, shape)
			assert.Equal(t, tt.wantCount, count)
		})
	}
}

func TestResolveTensorShape_DoesNotMutateInput(t *testing.T) {
	t.Parallel()
	dims := []int64{-1, 2, 3}
	_, _, err := resolveTensorShape(dims)
	require.NoError(t, err)
	assert.Equal(t, []int64{-1, 2, 3}, dims)
}

func TestRequireFloatTensor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		info    ort.InputOutputInfo
		wantErr string
	}{
		{name: "float32 tensor accepted", info: ort.InputOutputInfo{OrtValueType: ort.ONNXTypeTensor, DataType: ort.TensorElementDataTypeFloat}},
		{name: "int64 tensor rejected", info: ort.InputOutputInfo{OrtValueType: ort.ONNXTypeTensor, DataType: ort.TensorElementDataTypeInt64}, wantErr: "expected float32"},
		{name: "float16 tensor rejected", info: ort.InputOutputInfo{OrtValueType: ort.ONNXTypeTensor, DataType: ort.TensorElementDataTypeFloat16}, wantErr: "expected float32"},
		{name: "sequence rejected", info: ort.InputOutputInfo{OrtValueType: ort.ONNXTypeSequence, DataType: ort.TensorElementDataTypeFloat}, wantErr: "expected a tensor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireFloatTensor(&tt.info)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			var detErr *ModelDetectionError
			assert.ErrorAs(t, err, &detErr)
		})
	}
}

// newTestTensorModel returns a TensorModel over session with a [1, 4] input,
// for the Run and Close paths that return before any tensor is created.
func newTestTensorModel(session sessionRunner) *TensorModel {
	return &TensorModel{
		session:    guardedSession{s: session},
		inputShape: ort.Shape{1, 4},
		inputSize:  4,
	}
}

func TestTensorModelRun_InputSizeMismatch(t *testing.T) {
	t.Parallel()
	m := newTestTensorModel(&fakeSession{})

	_, err := m.Run(make([]float32, 3))
	var sizeErr *TensorInputSizeError
	require.ErrorAs(t, err, &sizeErr)
	assert.Equal(t, TensorInputSizeError{Expected: 4, Got: 3}, *sizeErr)
}

func TestTensorModelClose(t *testing.T) {
	t.Parallel()
	fake := &fakeSession{}
	m := newTestTensorModel(fake)

	require.NoError(t, m.Close())
	require.NoError(t, m.Close(), "a second Close must be a no-op")
	assert.Equal(t, int32(1), fake.destroys.Load(), "the session must be destroyed exactly once")

	_, err := m.Run(make([]float32, 4))
	require.ErrorIs(t, err, ErrSessionClosed)
}

func TestTensorModelClose_ReturnsDestroyError(t *testing.T) {
	t.Parallel()
	m := newTestTensorModel(&fakeSession{destroyErr: errFakeDestroy})

	require.ErrorIs(t, m.Close(), errFakeDestroy)
	require.NoError(t, m.Close(), "Close after a failed destroy must not destroy again")
}
