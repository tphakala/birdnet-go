package bsgbat

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeTolerance bounds the float32 rounding difference between the
// reference's numpy float32 arithmetic and the float64 accumulation here.
const normalizeTolerance = 1e-6

// TestNormalize pins the BSG-BAT per-window normalization against values computed
// once with the reference code (data384.py normalize: z-score over the whole
// matrix with the population standard deviation, subtract the per-mel-bin median
// over frames, clip to [0, 6]).
func TestNormalize(t *testing.T) {
	t.Parallel()

	// A single large value among zeros: 50 frames by 1 mel, so the outlier's
	// z-score (7) lies above the upper clip bound.
	const clipFrames = 50
	clipIn := make([]float32, clipFrames)
	clipIn[0] = 1
	clipWant := make([]float32, clipFrames)
	clipWant[0] = ClipMax

	tests := []struct {
		name string
		mels int
		in   []float32
		want []float32
	}{
		{
			// Even frame count (4): the median of a bin is the mean of its two
			// middle values. Sample (ddof 1) instead of population std, a wrong
			// median or a median over the wrong axis each change these values.
			name: "even frames, population std and per-bin median",
			mels: 3,
			in: []float32{
				-6.0, -5.0, -1.0,
				-4.0, -6.0, 0.5,
				-3.0, -5.5, 2.0,
				-6.0, -2.0, -3.0,
			},
			want: []float32{
				0, 0.09697622, 0,
				0.3879049, 0, 0.29092872,
				0.7758098, 0, 0.87278605,
				0, 1.2606909, 0,
			},
		},
		{
			name: "values above the upper bound are clipped",
			mels: 1,
			in:   clipIn,
			want: clipWant,
		},
		{
			// Digital silence is a constant matrix; the reference divides by a zero
			// standard deviation and yields NaN, here it must be all zeros.
			name: "constant matrix gives zeros",
			mels: 2,
			in:   []float32{-6, -6, -6, -6, -6, -6},
			want: []float32{0, 0, 0, 0, 0, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := slices.Clone(tt.in)
			Normalize(data, tt.mels)
			require.Len(t, data, len(tt.want))
			for i := range data {
				require.Falsef(t, math.IsNaN(float64(data[i])), "value %d is NaN", i)
				assert.InDeltaf(t, tt.want[i], data[i], normalizeTolerance, "value %d", i)
			}
		})
	}
}
