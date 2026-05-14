// Package ultrasonic provides a post-detection validation filter for bat detections.
// It measures temporal variability of ultrasonic energy (US frame CV) in source audio
// to distinguish real bat echolocation (high CV) from false positives (low CV).
package ultrasonic

import (
	"math"
	"math/cmplx"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ComputeUSFrameCV computes the coefficient of variation of per-frame ultrasonic
// energy for the given PCM audio samples. Returns the CV value and whether the
// computation succeeded (false if insufficient data or invalid parameters).
//
// samples contains the raw audio at sampleRate Hz. The function runs an STFT
// with the configured FFT/hop sizes, sums energy above the frequency split for
// each frame, and returns std(framePowers)/mean(framePowers).
func ComputeUSFrameCV(samples []float64, sampleRate int, cfg conf.UltrasonicFilterConfig) (float64, bool) {
	if len(samples) < cfg.FFTSize || sampleRate <= 0 || cfg.FFTSize <= 0 || cfg.HopSize <= 0 {
		return 0, false
	}

	if cfg.FrequencySplitHz >= sampleRate/2 {
		return 0, false
	}

	window := hanningWindow(cfg.FFTSize)
	numFrames := (len(samples) - cfg.FFTSize) / cfg.HopSize
	if numFrames < 2 {
		return 0, false
	}

	binWidth := float64(sampleRate) / float64(cfg.FFTSize)
	splitBin := int(float64(cfg.FrequencySplitHz) / binWidth)
	nyquistBin := cfg.FFTSize / 2

	framePowers := make([]float64, numFrames)
	buf := make([]complex128, cfg.FFTSize)

	for frame := range numFrames {
		offset := frame * cfg.HopSize
		for i := range cfg.FFTSize {
			buf[i] = complex(samples[offset+i]*window[i], 0)
		}
		fft(buf)

		var power float64
		for bin := splitBin; bin <= nyquistBin; bin++ {
			mag := cmplx.Abs(buf[bin]) * 2.0 / float64(cfg.FFTSize)
			if bin == 0 || bin == nyquistBin {
				mag /= 2.0
			}
			power += mag * mag
		}
		framePowers[frame] = power
	}

	return coefficientOfVariation(framePowers), true
}

// IsUnlikely returns true if the given US frame CV falls below the configured
// threshold, indicating the audio lacks ultrasonic temporal variability
// consistent with bat echolocation.
func IsUnlikely(cv float64, cfg conf.UltrasonicFilterConfig) bool {
	return cv < cfg.CVThreshold
}

// PCM16ToFloat64 converts 16-bit signed PCM bytes (little-endian, mono) to float64 samples
// normalized to [-1.0, 1.0].
func PCM16ToFloat64(data []byte) []float64 {
	numSamples := len(data) / 2
	samples := make([]float64, numSamples)
	for i := range numSamples {
		lo := data[2*i]
		hi := data[2*i+1]
		raw := int16(uint16(lo) | uint16(hi)<<8)
		samples[i] = float64(raw) / 32768.0
	}
	return samples
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

// fft performs an in-place iterative Cooley-Tukey FFT on data.
// len(data) must be a power of 2.
func fft(data []complex128) {
	n := len(data)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}

	// Butterfly stages
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		wn := cmplx.Rect(1, -2.0*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := range half {
				u := data[start+k]
				v := w * data[start+k+half]
				data[start+k] = u + v
				data[start+k+half] = u - v
				w *= wn
			}
		}
	}
}

// hanningWindow returns a Hanning window of length n.
func hanningWindow(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}
