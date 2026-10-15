// Package ultrasonic provides a post-detection validation filter for bat detections.
// It measures temporal variability of ultrasonic energy (US frame CV) in source audio
// to distinguish real bat echolocation (high CV) from false positives (low CV).
package ultrasonic

import (
	"math"

	"github.com/tphakala/go-audio-dsp/stft"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// HasUltrasonicBand reports whether a source at sampleRate Hz has any band above
// splitHz: the rate must be positive and the split strictly below Nyquist
// (sampleRate/2). The comparison is exact, so an odd rate keeps a split just
// under its half.
func HasUltrasonicBand(sampleRate, splitHz int) bool {
	return sampleRate > 0 && splitHz*2 < sampleRate
}

// ComputeUSFrameCV computes the coefficient of variation of per-frame ultrasonic
// energy for the given PCM audio samples. Returns the CV value and whether the
// computation succeeded (false if insufficient data or invalid parameters).
//
// samples are float32 audio in [-1, 1) at sampleRate Hz. The function runs an
// STFT with the configured FFT and hop sizes using the go-audio-dsp stft
// package (float32 FFT, symmetric Hann window, no padding), removes each
// frame's mean first so a DC offset cannot leak roundoff into the ultrasonic
// bins, sums the power above the frequency split for each frame in float64, and
// returns std(framePowers)/mean(framePowers). The result agrees with a float64
// FFT to about 2e-6 absolute.
//
// It returns false for any configuration cfg.ValidateSTFT rejects, for a
// FrequencySplitHz that leaves no band (see HasUltrasonicBand), and when samples hold fewer than two
// frames.
func ComputeUSFrameCV(samples []float32, sampleRate int, cfg conf.UltrasonicFilterConfig) (float64, bool) {
	if cfg.ValidateSTFT() != nil {
		return 0, false
	}
	if len(samples) < cfg.FFTSize || !HasUltrasonicBand(sampleRate, cfg.FrequencySplitHz) {
		return 0, false
	}

	numFrames := 1 + (len(samples)-cfg.FFTSize)/cfg.HopSize
	if numFrames < 2 {
		return 0, false
	}

	plan, err := stft.New(stft.Config{FrameSize: cfg.FFTSize, HopSize: cfg.HopSize, Window: stft.HannSymmetric})
	if err != nil {
		return 0, false
	}

	binWidth := float64(sampleRate) / float64(cfg.FFTSize)
	splitBin := int(float64(cfg.FrequencySplitHz) / binWidth)
	nyquistBin := cfg.FFTSize / 2

	framePowers := make([]float64, numFrames)
	frame := make([]float32, cfg.FFTSize)
	power := make([]float32, plan.NumBins())

	for f := range numFrames {
		offset := f * cfg.HopSize
		removeMean(frame, samples[offset:offset+cfg.FFTSize])
		plan.PowerInto(power, frame, stft.NoPad)

		var sum float64
		for bin := splitBin; bin <= nyquistBin; bin++ {
			p := float64(power[bin])
			if bin > 0 && bin < nyquistBin {
				p *= 2
			}
			sum += p
		}
		framePowers[f] = sum
	}

	return coefficientOfVariation(framePowers), true
}

// removeMean writes src minus its mean into dst. The mean is accumulated in
// float64; dst and src have the same length.
func removeMean(dst, src []float32) {
	var sum float64
	for _, v := range src {
		sum += float64(v)
	}
	mean := float32(sum / float64(len(src)))
	for i, v := range src {
		dst[i] = v - mean
	}
}

// IsUnlikely returns true if the given US frame CV falls below the configured
// threshold, indicating the audio lacks ultrasonic temporal variability
// consistent with bat echolocation.
func IsUnlikely(cv float64, cfg conf.UltrasonicFilterConfig) bool {
	return cv < cfg.CVThreshold
}

// coefficientOfVariation returns std/mean for the given values.
func coefficientOfVariation(values []float64) float64 {
	n := float64(len(values))
	if n < 2 {
		return 0
	}

	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / n
	if mean <= 0 {
		return 0
	}

	var sumSq float64
	for _, v := range values {
		d := v - mean
		sumSq += d * d
	}
	return math.Sqrt(sumSq/n) / mean
}
