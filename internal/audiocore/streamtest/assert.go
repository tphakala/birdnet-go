package streamtest

import (
	"math"

	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
)

// Signal-analysis constants for the tone assertions.
const (
	// silenceFloorDBFS is returned by RMSDBFS for input with no measurable
	// energy, so callers get a finite, deeply negative value instead of -Inf.
	silenceFloorDBFS = -200.0

	// coarseScanStepHz and fineScanStepHz control the two-pass Goertzel sweep in
	// DominantFrequency: a coarse sweep finds the rough peak, a fine sweep around
	// it pins the frequency to within the 1% tolerance the tone checks need.
	coarseScanStepHz = 5.0
	fineScanStepHz   = 0.5

	// scanLowHz and scanHighHz bound the search. They comfortably contain the
	// 1 kHz reference tone and its low harmonics while excluding DC drift and
	// codec artefacts near Nyquist.
	scanLowHz  = 50.0
	scanHighHz = 8000.0

	// fineScanHalfWindowHz is the half-width of the fine sweep around the coarse
	// peak.
	fineScanHalfWindowHz = 2 * coarseScanStepHz

	// minScanFreqHz is the lowest frequency the fine sweep will probe, keeping the
	// scan above DC even when the coarse peak sits near the low bound.
	minScanFreqHz = 1.0
)

// samplesFromS16LE decodes mono s16le PCM into float64 samples in [-1, 1) using
// the same convention the production pipeline uses (convert.BytesToFloat64PCM16),
// so the characterization measurement never drifts from the real decode.
func samplesFromS16LE(pcm []byte) []float64 {
	return convert.BytesToFloat64PCM16(pcm)
}

// goertzelPower returns the (unnormalised) power of frequency freq in samples
// taken at sampleRate, via the Goertzel algorithm.
func goertzelPower(samples []float64, freq float64, sampleRate int) float64 {
	if len(samples) == 0 || sampleRate <= 0 {
		return 0
	}
	omega := 2 * math.Pi * freq / float64(sampleRate)
	coeff := 2 * math.Cos(omega)
	var s0, s1, s2 float64
	for _, x := range samples {
		s0 = x + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

// scanPeak returns the frequency in [low, high] (stepping by step) whose
// Goertzel power is greatest, clamping high to just under Nyquist.
func scanPeak(samples []float64, sampleRate int, low, high, step float64) float64 {
	nyquist := float64(sampleRate) / 2
	if high > nyquist {
		high = nyquist - step
	}
	bestFreq := low
	bestPower := math.Inf(-1)
	for f := low; f <= high; f += step {
		if p := goertzelPower(samples, f, sampleRate); p > bestPower {
			bestPower = p
			bestFreq = f
		}
	}
	return bestFreq
}

// DominantFrequency returns the frequency in Hz carrying the most energy in
// mono s16le PCM sampled at sampleRate. It runs a coarse Goertzel sweep across
// the audible band followed by a fine sweep around the peak, which is accurate
// enough to confirm a published test tone survives the ingest pipeline. It
// returns 0 when there is less than one full sample.
func DominantFrequency(pcm []byte, sampleRate int) float64 {
	samples := samplesFromS16LE(pcm)
	if len(samples) == 0 || sampleRate <= 0 {
		return 0
	}
	coarse := scanPeak(samples, sampleRate, scanLowHz, scanHighHz, coarseScanStepHz)
	low := math.Max(coarse-fineScanHalfWindowHz, minScanFreqHz)
	high := coarse + fineScanHalfWindowHz
	return scanPeak(samples, sampleRate, low, high, fineScanStepHz)
}

// RMSDBFS returns the RMS level of mono s16le PCM in dBFS, where 0 dBFS is a
// full-scale int16. Silence and empty input return silenceFloorDBFS rather than
// negative infinity.
func RMSDBFS(pcm []byte) float64 {
	samples := samplesFromS16LE(pcm)
	if len(samples) == 0 {
		return silenceFloorDBFS
	}
	rms := math.Sqrt(convert.SumOfSquaresFloat64(samples) / float64(len(samples)))
	if rms <= 0 {
		return silenceFloorDBFS
	}
	db := 20 * math.Log10(rms)
	return math.Max(db, silenceFloorDBFS)
}
