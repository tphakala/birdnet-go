package audionorm

import (
	"math"
	"slices"
	"testing"
)

func TestApplyGainFloat32(t *testing.T) {
	pcm := []float32{0.1, -0.2, 0.3, -0.05}
	applyGainFloat32(pcm, 2.0)
	want := []float32{0.2, -0.4, 0.6, -0.1}
	for i := range pcm {
		if math.Abs(float64(pcm[i]-want[i])) > 1e-6 {
			t.Errorf("pcm[%d] = %g, want %g", i, pcm[i], want[i])
		}
	}
}

func TestApplyGainInt16ScalesAndRounds(t *testing.T) {
	pcm := []int16{100, -100, 10000, -10000}
	applyGainInt16(pcm, 2.0)
	want := []int16{200, -200, 20000, -20000}
	for i := range pcm {
		if pcm[i] != want[i] {
			t.Errorf("pcm[%d] = %d, want %d", i, pcm[i], want[i])
		}
	}
}

// Boosting past full scale must saturate, never wrap around.
func TestApplyGainInt16Saturates(t *testing.T) {
	pcm := []int16{20000, -20000, 32767, -32768}
	applyGainInt16(pcm, 4.0)
	want := []int16{32767, -32768, 32767, -32768}
	for i := range pcm {
		if pcm[i] != want[i] {
			t.Errorf("pcm[%d] = %d, want %d (must saturate, not wrap)", i, pcm[i], want[i])
		}
	}
}

// Half-integer results must round to even (matching the documented behavior),
// not truncate or round half-up.
func TestApplyGainInt16RoundsHalfToEven(t *testing.T) {
	pcm := []int16{1, 3, 5, 7}
	applyGainInt16(pcm, 2.5) // -> 2.5, 7.5, 12.5, 17.5
	want := []int16{2, 8, 12, 18}
	for i := range pcm {
		if pcm[i] != want[i] {
			t.Errorf("pcm[%d] = %d, want %d (round half to even)", i, pcm[i], want[i])
		}
	}
}

func TestApplyGainInt16Unity(t *testing.T) {
	pcm := []int16{1, -1, 12345, -12345, 32767, -32768}
	orig := slices.Clone(pcm)
	applyGainInt16(pcm, 1.0)
	for i := range pcm {
		if pcm[i] != orig[i] {
			t.Errorf("unity gain changed pcm[%d]: %d -> %d", i, orig[i], pcm[i])
		}
	}
}
