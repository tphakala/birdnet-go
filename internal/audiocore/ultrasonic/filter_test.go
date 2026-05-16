package ultrasonic

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func defaultFilterConfig() conf.UltrasonicFilterConfig {
	return conf.UltrasonicFilterConfig{
		Enabled:          true,
		CVThreshold:      0.15,
		FFTSize:          8192,
		HopSize:          4096,
		FrequencySplitHz: 20000,
	}
}

func TestComputeUSFrameCV_FlatUltrasonicTone(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	sampleRate := 256000
	numSamples := 144000

	// Generate a continuous ultrasonic tone at constant amplitude.
	// This simulates a false positive scenario where ultrasonic energy
	// is flat across all frames (e.g., clock harmonic or constant noise floor).
	samples := make([]float64, numSamples)
	freq := 40000.0 // 40kHz constant tone above the 20kHz split
	for i := range samples {
		ti := float64(i) / float64(sampleRate)
		samples[i] = 0.01 * math.Sin(2*math.Pi*freq*ti)
	}

	cv, ok := ComputeUSFrameCV(samples, sampleRate, cfg)
	require.True(t, ok, "computation should succeed")
	assert.Less(t, cv, cfg.CVThreshold, "constant tone should have low CV (got %f)", cv)
}

func TestComputeUSFrameCV_UltrasonicBurst(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	sampleRate := 256000
	numSamples := 144000

	// Generate silence with a burst of ultrasonic energy in the middle
	// (simulates bat echolocation call)
	samples := make([]float64, numSamples)
	burstFreq := 45000.0 // 45kHz, well above the 20kHz split
	burstStart := numSamples / 3
	burstEnd := 2 * numSamples / 3

	for i := burstStart; i < burstEnd; i++ {
		t := float64(i) / float64(sampleRate)
		samples[i] = 0.5 * math.Sin(2*math.Pi*burstFreq*t)
	}

	cv, ok := ComputeUSFrameCV(samples, sampleRate, cfg)
	require.True(t, ok, "computation should succeed")
	assert.Greater(t, cv, cfg.CVThreshold, "ultrasonic burst should have high CV (got %f)", cv)
}

func TestComputeUSFrameCV_InsufficientData(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()

	_, ok := ComputeUSFrameCV(make([]float64, 100), 256000, cfg)
	assert.False(t, ok, "should fail with fewer samples than FFT size")
}

func TestComputeUSFrameCV_FrequencySplitAboveNyquist(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	cfg.FrequencySplitHz = 30000

	// At 48kHz, Nyquist is 24kHz, so 30kHz split is above Nyquist
	_, ok := ComputeUSFrameCV(make([]float64, 20000), 48000, cfg)
	assert.False(t, ok, "should fail when frequency split >= Nyquist")
}

func TestComputeUSFrameCV_NonPowerOfTwoFFTSize(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	cfg.FFTSize = 6000

	_, ok := ComputeUSFrameCV(make([]float64, 20000), 256000, cfg)
	assert.False(t, ok, "should fail when FFTSize is not a power of 2")
}

func TestComputeUSFrameCV_TooFewFrames(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()

	// Exactly FFTSize samples = 0 frames with hop
	_, ok := ComputeUSFrameCV(make([]float64, cfg.FFTSize), 256000, cfg)
	assert.False(t, ok, "should fail with too few STFT frames")

	// FFTSize + HopSize samples = 2 frames (with +1 formula), which is the minimum
	_, ok = ComputeUSFrameCV(make([]float64, cfg.FFTSize+cfg.HopSize), 256000, cfg)
	assert.True(t, ok, "FFTSize + HopSize should yield 2 frames (minimum for CV)")
}

func TestIsUnlikely(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()

	assert.True(t, IsUnlikely(0.05, cfg), "CV 0.05 should be unlikely")
	assert.True(t, IsUnlikely(0.14, cfg), "CV 0.14 should be unlikely")
	assert.False(t, IsUnlikely(0.15, cfg), "CV exactly at threshold should not be unlikely")
	assert.False(t, IsUnlikely(0.50, cfg), "CV 0.50 should not be unlikely")
}

func TestHanningWindow(t *testing.T) {
	t.Parallel()

	w := hanningWindow(8)
	require.Len(t, w, 8)

	// Hanning window should be 0 at endpoints and 1 at center
	assert.InDelta(t, 0.0, w[0], 1e-10, "first sample should be ~0")
	assert.InDelta(t, 0.0, w[7], 1e-10, "last sample should be ~0")

	// Peak near center
	maxIdx := 0
	for i, v := range w {
		if v > w[maxIdx] {
			maxIdx = i
		}
	}
	assert.True(t, maxIdx == 3 || maxIdx == 4, "peak should be near center, got index %d", maxIdx)
}

func TestFFT_KnownSinusoid(t *testing.T) {
	t.Parallel()

	n := 256
	data := make([]complex128, n)
	// Single sinusoid at bin 10
	for i := range n {
		data[i] = complex(math.Sin(2*math.Pi*10*float64(i)/float64(n)), 0)
	}

	fft(data)

	// Find peak magnitude bin
	maxMag := 0.0
	maxBin := 0
	for i := range n / 2 {
		mag := math.Sqrt(real(data[i])*real(data[i]) + imag(data[i])*imag(data[i]))
		if mag > maxMag {
			maxMag = mag
			maxBin = i
		}
	}

	assert.Equal(t, 10, maxBin, "peak should be at bin 10")
	assert.Greater(t, maxMag, 50.0, "peak magnitude should be significant")
}

func TestCoefficientOfVariation(t *testing.T) {
	t.Parallel()

	// Constant values should have CV = 0
	assert.InDelta(t, 0.0, coefficientOfVariation([]float64{5, 5, 5, 5}), 1e-10)

	// Known CV: mean=2, values 1,2,3 -> std = sqrt(2/3) -> CV = sqrt(2/3)/2
	cv := coefficientOfVariation([]float64{1, 2, 3})
	expected := math.Sqrt(2.0/3.0) / 2.0
	assert.InDelta(t, expected, cv, 1e-10)

	// Too few values
	assert.InDelta(t, 0.0, coefficientOfVariation([]float64{1}), 1e-10)
	assert.InDelta(t, 0.0, coefficientOfVariation(nil), 1e-10)
}

func TestComputeUSFrameCV_ScaleInvariance(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	sampleRate := 256000
	numSamples := 144000

	// Generate a signal with ultrasonic content
	samples := make([]float64, numSamples)
	burstFreq := 45000.0
	for i := numSamples / 4; i < numSamples/2; i++ {
		ti := float64(i) / float64(sampleRate)
		samples[i] = 0.5 * math.Sin(2*math.Pi*burstFreq*ti)
	}

	cv1, ok1 := ComputeUSFrameCV(samples, sampleRate, cfg)
	require.True(t, ok1)

	// Scale all samples by 10x
	scaled := make([]float64, numSamples)
	for i, s := range samples {
		scaled[i] = s * 10.0
	}

	cv2, ok2 := ComputeUSFrameCV(scaled, sampleRate, cfg)
	require.True(t, ok2)

	assert.InDelta(t, cv1, cv2, 0.01, "CV should be scale-invariant (cv1=%f, cv2=%f)", cv1, cv2)
}

func BenchmarkComputeUSFrameCV(b *testing.B) {
	cfg := defaultFilterConfig()
	sampleRate := 256000
	numSamples := 144000

	samples := make([]float64, numSamples)
	for i := range samples {
		samples[i] = 0.01 * math.Sin(float64(i)*0.1)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ComputeUSFrameCV(samples, sampleRate, cfg)
	}
}
