package streamtest

import (
	"math"

	dsppcm "github.com/tphakala/go-audio-dsp/pcm"
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

	// bytesPerSample is the size of one 16-bit PCM sample in bytes.
	bytesPerSample = 2
)

// samplesFromS16LE decodes mono s16le PCM into float32 samples in [-1, 1) with
// the go-audio-dsp pcm package, the decode the production pipeline uses, so the
// characterization measurement never drifts from the real decode. It returns
// nil for a buffer with a half sample at the end: DominantFrequency then
// returns 0 and RMSDBFS returns silenceFloorDBFS.
func samplesFromS16LE(pcm []byte) []float32 {
	if len(pcm)%bytesPerSample != 0 {
		return nil
	}
	samples := make([]float32, len(pcm)/bytesPerSample)
	if _, err := dsppcm.BytesToFloat32(samples, pcm); err != nil {
		return nil
	}
	return samples
}

// goertzelPower returns the (unnormalised) power of frequency freq in samples
// taken at sampleRate, via the Goertzel algorithm.
func goertzelPower(samples []float32, freq float64, sampleRate int) float64 {
	if len(samples) == 0 || sampleRate <= 0 {
		return 0
	}
	omega := 2 * math.Pi * freq / float64(sampleRate)
	coeff := 2 * math.Cos(omega)
	var s0, s1, s2 float64
	for _, sample := range samples {
		s0 = float64(sample) + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}
	return s1*s1 + s2*s2 - coeff*s1*s2
}

// scanPeak returns the frequency in [low, high] (stepping by step) whose
// Goertzel power is greatest, clamping high to just under Nyquist.
func scanPeak(samples []float32, sampleRate int, low, high, step float64) float64 {
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
// returns 0 when there is less than one full sample or the buffer ends in a
// half sample.
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
// full-scale int16. Silence, empty input and a buffer that ends in a half sample
// return silenceFloorDBFS rather than negative infinity.
func RMSDBFS(pcm []byte) float64 {
	samples := samplesFromS16LE(pcm)
	if len(samples) == 0 {
		return silenceFloorDBFS
	}
	var sumSquares float64
	for _, sample := range samples {
		v := float64(sample)
		sumSquares += v * v
	}
	rms := math.Sqrt(sumSquares / float64(len(samples)))
	if rms <= 0 {
		return silenceFloorDBFS
	}
	db := 20 * math.Log10(rms)
	return math.Max(db, silenceFloorDBFS)
}
