package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPredictBatchRaw_EmptyBatch(t *testing.T) {
	t.Parallel()

	r := &RangeFilter{labels: make([]string, 10)}

	_, err := r.PredictBatchRaw([]float32{1, 2, 3}, 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyRangeFilterBatch)
}

func TestPredictBatchRaw_NegativeBatchSize(t *testing.T) {
	t.Parallel()

	r := &RangeFilter{labels: make([]string, 10)}

	_, err := r.PredictBatchRaw([]float32{1, 2, 3}, -1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyRangeFilterBatch)
}

func TestPredictBatchRaw_InputLengthMismatch(t *testing.T) {
	t.Parallel()

	r := &RangeFilter{labels: make([]string, 10)}

	tests := []struct {
		name      string
		inputs    []float32
		batchSize int
		wantGot   int
		wantExp   int
	}{
		{
			name:      "too few inputs",
			inputs:    []float32{1, 2},
			batchSize: 1,
			wantGot:   2,
			wantExp:   3,
		},
		{
			name:      "too many inputs",
			inputs:    []float32{1, 2, 3, 4},
			batchSize: 1,
			wantGot:   4,
			wantExp:   3,
		},
		{
			name:      "batch of 3 with wrong count",
			inputs:    []float32{1, 2, 3, 4, 5, 6, 7},
			batchSize: 3,
			wantGot:   7,
			wantExp:   9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := r.PredictBatchRaw(tt.inputs, tt.batchSize)
			require.Error(t, err)

			var batchErr *RangeFilterBatchInputError
			require.ErrorAs(t, err, &batchErr)
			assert.Equal(t, tt.wantExp, batchErr.Expected)
			assert.Equal(t, tt.wantGot, batchErr.Got)
		})
	}
}

func TestRangeFilterBatchInputError_Message(t *testing.T) {
	t.Parallel()

	err := &RangeFilterBatchInputError{Expected: 9, Got: 7}
	assert.Contains(t, err.Error(), "7 values")
	assert.Contains(t, err.Error(), "expected 9")
}

func TestErrEmptyRangeFilterBatch_Message(t *testing.T) {
	t.Parallel()

	assert.Contains(t, ErrEmptyRangeFilterBatch.Error(), "at least one input")
}
