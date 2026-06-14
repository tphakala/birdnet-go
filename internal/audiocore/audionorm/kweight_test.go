package audionorm

import (
	"math"
	"testing"
)

// Published ITU-R BS.1770-4 digital coefficients at 48 kHz. Our bilinear
// transform must reproduce these from the analog prototype parameters.
func TestKWeightingStages48kMatchesPublished(t *testing.T) {
	s1, s2 := kWeightingStages(48000)

	const tol = 1e-6

	wantS1 := biquad{
		b0: 1.53512485958697,
		b1: -2.69169618940638,
		b2: 1.19839281085285,
		a1: -1.69065929318241,
		a2: 0.73248077421585,
	}
	wantS2 := biquad{
		b0: 1.0,
		b1: -2.0,
		b2: 1.0,
		a1: -1.99004745483398,
		a2: 0.99007225036621,
	}

	checkBiquad(t, "stage1", s1, wantS1, tol)
	checkBiquad(t, "stage2", s2, wantS2, tol)
}

func checkBiquad(t *testing.T, name string, got, want biquad, tol float64) {
	t.Helper()
	for _, c := range []struct {
		field    string
		got, exp float64
	}{
		{"b0", got.b0, want.b0},
		{"b1", got.b1, want.b1},
		{"b2", got.b2, want.b2},
		{"a1", got.a1, want.a1},
		{"a2", got.a2, want.a2},
	} {
		if math.Abs(c.got-c.exp) > tol {
			t.Errorf("%s.%s = %.14f, want %.14f (diff %.2e)", name, c.field, c.got, c.exp, math.Abs(c.got-c.exp))
		}
	}
}

// A 1 kHz tone should pass the K-weighting filter with ~+0.69 dB gain, which is
// the property that cancels the -0.691 LUFS calibration offset for a 1 kHz
// reference. This is the single most important filter property.
func TestKWeightingGainAt1kHz(t *testing.T) {
	const fs = 48000.0
	s1, s2 := kWeightingStages(fs)

	gainDB := biquadGainDB(s1, 1000, fs) + biquadGainDB(s2, 1000, fs)
	const want = 0.691
	if math.Abs(gainDB-want) > 0.05 {
		t.Errorf("K-weighting gain at 1 kHz = %.4f dB, want ~%.3f dB", gainDB, want)
	}
}

// biquadGainDB returns the magnitude response of a biquad at frequency f (Hz)
// for sample rate fs (Hz), in dB. Used only by tests.
func biquadGainDB(b biquad, f, fs float64) float64 {
	w := 2 * math.Pi * f / fs
	// H(e^jw) = (b0 + b1 e^-jw + b2 e^-2jw) / (1 + a1 e^-jw + a2 e^-2jw)
	cw, c2w := math.Cos(w), math.Cos(2*w)
	sw, s2w := math.Sin(w), math.Sin(2*w)
	numRe := b.b0 + b.b1*cw + b.b2*c2w
	numIm := -(b.b1*sw + b.b2*s2w)
	denRe := 1 + b.a1*cw + b.a2*c2w
	denIm := -(b.a1*sw + b.a2*s2w)
	numMag := math.Hypot(numRe, numIm)
	denMag := math.Hypot(denRe, denIm)
	return 20 * math.Log10(numMag/denMag)
}
