package onnx

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
