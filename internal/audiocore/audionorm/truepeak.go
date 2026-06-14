package audionorm

import (
	"math"

	simdf32 "github.com/tphakala/simd/f32"
)

// True-peak estimation per ITU-R BS.1770-4 Annex 2: oversample by 4x with a
// low-pass interpolation FIR and take the maximum absolute value of the
// reconstructed signal.
const (
	oversample   = 4  // 4x oversampling (sufficient for fs <= 48 kHz per BS.1770-4)
	tapsPerPhase = 32 // taps per polyphase branch
	protoLen     = oversample * tapsPerPhase
	kaiserBeta   = 9.0  // window beta: sharp passband to Nyquist, matches swresample defaults
	tpChunk      = 8192 // samples per SIMD convolution batch (bounds true-peak scratch)
)

// tpCoef[p][t] is the polyphase interpolation kernel: phase p (fractional
// position p/oversample), tap t weighting input sample x[k-t]. Built once from a
// Kaiser-windowed sinc with cutoff at the original Nyquist and normalized so
// each phase is a partition of unity (a constant input is reproduced exactly).
// A sharp Kaiser window keeps the passband flat up to Nyquist, so it captures
// the inter-sample overshoot of high-frequency content rather than rolling it
// off (which would under-report the true peak).
var tpCoef = buildTruePeakKernel()

// tpKernelRev holds each phase's coefficients reversed. simd ConvolveValid
// computes a correlation (dst[i] = sum(signal[i+j]*kernel[j])), so feeding it
// the reversed kernel yields the true convolution sum_t coef[t]*signal[k-t],
// which is exactly the per-sample polyphase output. This lets the whole
// oversampling pass run as batched SIMD convolutions instead of a scalar dot
// product per input sample.
var tpKernelRev = func() [oversample][tapsPerPhase]float32 {
	var r [oversample][tapsPerPhase]float32
	for p := range oversample {
		for t := range tapsPerPhase {
			r[p][t] = float32(tpCoef[p][tapsPerPhase-1-t])
		}
	}
	return r
}()

// tpKernelsRev is a slice-of-slices view of the reversed phase kernels, the form
// ConvolveValidMaxAbsMulti takes (one fused pass over all polyphase branches).
var tpKernelsRev = func() [][]float32 {
	ks := make([][]float32, oversample)
	for p := range oversample {
		ks[p] = tpKernelRev[p][:]
	}
	return ks
}()

func buildTruePeakKernel() [oversample][tapsPerPhase]float64 {
	proto := make([]float64, protoLen)
	center := float64(protoLen-1) / 2.0
	for n := range protoLen {
		x := (float64(n) - center) / float64(oversample)
		proto[n] = sinc(x) * kaiser(n, protoLen, kaiserBeta)
	}

	var k [oversample][tapsPerPhase]float64
	for p := range oversample {
		var sum float64
		for t := range tapsPerPhase {
			c := proto[p+oversample*t]
			k[p][t] = c
			sum += c
		}
		if sum != 0 {
			for t := range tapsPerPhase {
				k[p][t] /= sum
			}
		}
	}
	return k
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// kaiser returns the n-th sample of a length-`length` Kaiser window with shape
// parameter beta.
func kaiser(n, length int, beta float64) float64 {
	N := float64(length - 1)
	r := 2*float64(n)/N - 1 // -1 .. 1
	return besselI0(beta*math.Sqrt(1-r*r)) / besselI0(beta)
}

// besselI0 is the zeroth-order modified Bessel function of the first kind,
// evaluated by its rapidly converging power series.
func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	half := x / 2
	for k := 1; k < 40; k++ {
		term *= (half / float64(k)) * (half / float64(k))
		sum += term
		if term < 1e-15*sum {
			break
		}
	}
	return sum
}

// truePeakChannel estimates the true peak of one channel. Samples are processed
// in batches: hist carries the last tapsPerPhase-1 samples across batches so the
// oversampling filter spans batch boundaries seamlessly.
type truePeakChannel struct {
	hist   [tapsPerPhase - 1]float32
	maxAbs float32
}

// absF32 returns the absolute value of a float32 (math.Abs is float64-only).
func absF32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

// batch processes one sub-chunk of raw samples. sig is [history | new samples]
// with len(sig) == tapsPerPhase-1+n. It updates maxAbs with both the raw sample
// peak and the 4x-oversampled peak (the max across all polyphase branches,
// computed in one fused SIMD pass with no output materialization), then stores
// the last tapsPerPhase-1 samples as history for the next batch.
func (t *truePeakChannel) batch(sig []float32) {
	// Raw sample peak (vectorized), so the estimate never under-reports the input.
	if pk := simdf32.MaxAbs(sig[tapsPerPhase-1:]); pk > t.maxAbs {
		t.maxAbs = pk
	}
	// Oversampled peak: the max across all 4 polyphase branches in one fused pass
	// (dedicated SIMD kernel, no output materialization, signal read once).
	if pk := simdf32.ConvolveValidMaxAbsMulti(sig, tpKernelsRev); pk > t.maxAbs {
		t.maxAbs = pk
	}
	copy(t.hist[:], sig[len(sig)-(tapsPerPhase-1):])
}

// dbtp converts the linear maximum to dBTP (full scale = 1.0). It first drains
// the interpolator's group delay (about tapsPerPhase/2 samples) by sliding
// trailing zeros through the history, so inter-sample peaks in the final samples
// are captured. The drain runs on a stack-local copy and only reads state, so it
// allocates nothing and repeated calls are idempotent.
func (t *truePeakChannel) dbtp() float64 {
	maxAbs := t.maxAbs
	const drainLen = tapsPerPhase / 2
	var sig [tapsPerPhase - 1 + drainLen]float32 // history followed by zeros
	copy(sig[:tapsPerPhase-1], t.hist[:])
	for i := range drainLen {
		for p := range oversample {
			rev := &tpKernelRev[p]
			var y float32
			for j := range tapsPerPhase {
				y += sig[i+j] * rev[j]
			}
			if a := absF32(y); a > maxAbs {
				maxAbs = a
			}
		}
	}
	if maxAbs <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(float64(maxAbs))
}
