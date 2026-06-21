package audionorm

import "math"

// biquad is a second-order IIR section in transposed-direct-form-II-ready
// normalized coefficients. The difference equation is:
//
//	y[n] = b0*x[n] + b1*x[n-1] + b2*x[n-2] - a1*y[n-1] - a2*y[n-2]
//
// (a0 is normalized to 1).
type biquad struct {
	b0, b1, b2, a1, a2 float64
}

// K-weighting analog prototype parameters from ITU-R BS.1770-4. At 48 kHz the
// bilinear transform of these reproduces the published digital coefficients.
const (
	// Stage 1: high-shelf "head" pre-filter.
	kStage1F0   = 1681.974450955533
	kStage1Q    = 0.7071752369554196
	kStage1GdB  = 3.999843853973347
	kStage1VbEx = 0.4996667741545416 // exponent relating Vb to Vh

	// Stage 2: RLB high-pass filter.
	kStage2F0 = 38.13547087602444
	kStage2Q  = 0.5003270373238773
)

// kWeightingStages returns the two cascaded biquad sections of the BS.1770-4
// K-weighting filter for an arbitrary sample rate (Hz), derived via the
// bilinear transform of the analog prototypes.
func kWeightingStages(sampleRate float64) (stage1, stage2 biquad) {
	stage1 = kHighShelf(sampleRate)
	stage2 = kHighPass(sampleRate)
	return stage1, stage2
}

// kHighShelf builds stage 1, the high-frequency shelving filter.
func kHighShelf(fs float64) biquad {
	K := math.Tan(math.Pi * kStage1F0 / fs)
	Vh := math.Pow(10.0, kStage1GdB/20.0)
	Vb := math.Pow(Vh, kStage1VbEx)
	K2 := K * K
	a0 := 1.0 + K/kStage1Q + K2
	return biquad{
		b0: (Vh + Vb*K/kStage1Q + K2) / a0,
		b1: 2.0 * (K2 - Vh) / a0,
		b2: (Vh - Vb*K/kStage1Q + K2) / a0,
		a1: 2.0 * (K2 - 1.0) / a0,
		a2: (1.0 - K/kStage1Q + K2) / a0,
	}
}

// kHighPass builds stage 2, the RLB high-pass filter. Its numerator is fixed at
// (1 - z^-1)^2, i.e. b = {1, -2, 1}, per BS.1770-4; only the denominator depends
// on the sample rate.
func kHighPass(fs float64) biquad {
	K := math.Tan(math.Pi * kStage2F0 / fs)
	K2 := K * K
	a0 := 1.0 + K/kStage2Q + K2
	return biquad{
		b0: 1.0,
		b1: -2.0,
		b2: 1.0,
		a1: 2.0 * (K2 - 1.0) / a0,
		a2: (1.0 - K/kStage2Q + K2) / a0,
	}
}
