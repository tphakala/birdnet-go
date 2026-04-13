package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpectrogramBinsMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		bins SpectrogramBins
		want string
	}{
		{"empty", SpectrogramBins{}, "[]"},
		{"nil", nil, "[]"},
		{"single_zero", SpectrogramBins{0}, "[0]"},
		{"single_max", SpectrogramBins{255}, "[255]"},
		{"three_values", SpectrogramBins{0, 128, 255}, "[0,128,255]"},
		{"all_digit_widths", SpectrogramBins{1, 12, 123, 9, 99, 200}, "[1,12,123,9,99,200]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.bins.MarshalJSON()
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))

			// Round-trip via the stdlib decoder to confirm the output is
			// valid JSON and numerically identical to the input. Compare
			// element-wise so a nil input matches an empty decoded slice.
			var decoded []uint8
			require.NoError(t, json.Unmarshal(got, &decoded))
			assert.Len(t, decoded, len(tt.bins))
			for i := range tt.bins {
				assert.Equal(t, tt.bins[i], decoded[i])
			}
		})
	}
}

func TestSpectrogramBinsMarshalJSONLarge(t *testing.T) {
	t.Parallel()

	// Simulate a realistic column: fftSize=1024 → 512 bins.
	bins := make(SpectrogramBins, 512)
	for i := range bins {
		bins[i] = uint8(i & 0xff)
	}

	got, err := bins.MarshalJSON()
	require.NoError(t, err)

	var decoded []uint8
	require.NoError(t, json.Unmarshal(got, &decoded))
	assert.Equal(t, []uint8(bins), decoded)
}

func BenchmarkSpectrogramBinsMarshalJSON(b *testing.B) {
	bins := make(SpectrogramBins, 512)
	for i := range bins {
		bins[i] = uint8(i & 0xff)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = bins.MarshalJSON()
	}
}
