// internal/birdweather/soundscape_gain_test.go
package birdweather

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSoundscapeGainDB verifies that a non-finite loudness measurement (ffmpeg
// reports input_i as "-inf" for a silent clip) yields a finite 0 dB gain
// instead of +Inf, while finite measurements map to target - inputLUFS.
func TestSoundscapeGainDB(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"silent clip (-inf) left untouched", math.Inf(-1), 0},
		{"positive inf guarded", math.Inf(1), 0},
		{"nan guarded", math.NaN(), 0},
		{"quiet clip needs positive gain", -70.0, targetIntegratedLoudnessLUFS - (-70.0)},
		{"already at target needs no gain", targetIntegratedLoudnessLUFS, 0},
		{"loud clip needs negative gain", 0.0, targetIntegratedLoudnessLUFS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := soundscapeGainDB(tt.input)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("soundscapeGainDB(%v) returned non-finite %v", tt.input, got)
			}
			assert.Equal(t, tt.want, got, "soundscapeGainDB(%v) mismatch", tt.input)
		})
	}
}
