package ultrasonic

import (
	"fmt"
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// ultrasonicCVAbsTol bounds |cv - reference| for the float32 STFT against the
	// float64 reference. The measured worst case on synthetic inputs is 2.1e-6.
	ultrasonicCVAbsTol = 1e-5

	// thresholdClearance is how far a reference CV must sit from the verdict
	// threshold for the verdict comparison to be meaningful.
	thresholdClearance = 1e-6

	// referenceSamples is the clip length of the reference signals (about 0.56 s
	// at 256 kHz, the size of a bat analysis chunk).
	referenceSamples = 144000

	// pcm16Scale is the int16 full scale used to quantise the test signals.
	pcm16Scale = 32768.0
)

// quantize rounds v (nominal [-1, 1)) to an int16 grid and returns it as float32,
// so the signals carry the same quantisation as decoded PCM16.
func quantize(v float64) float32 {
	q := math.Round(v * pcm16Scale)
	q = math.Max(-pcm16Scale, math.Min(pcm16Scale-1, q))
	return float32(q / pcm16Scale)
}

// referenceCV is the previous in-tree algorithm run in float64: a complex128
// radix-2 FFT, a symmetric Hann window, the x2 one-sided weighting and, when
// removeMean is set, per-frame mean removal.
func referenceCV(samples []float32, sampleRate int, cfg conf.UltrasonicFilterConfig, removeMean bool) float64 {
	n := cfg.FFTSize
	numFrames := 1 + (len(samples)-n)/cfg.HopSize
	binWidth := float64(sampleRate) / float64(n)
	splitBin := int(float64(cfg.FrequencySplitHz) / binWidth)
	powers := make([]float64, numFrames)
	buf := make([]complex128, n)
	for frame := range numFrames {
		off := frame * cfg.HopSize
		var mean float64
		if removeMean {
			for i := range n {
				mean += float64(samples[off+i])
			}
			mean /= float64(n)
		}
		for i := range n {
			w := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
			buf[i] = complex((float64(samples[off+i])-mean)*w, 0)
		}
		referenceFFT(buf)
		var power float64
		for bin := splitBin; bin <= n/2; bin++ {
			p := real(buf[bin])*real(buf[bin]) + imag(buf[bin])*imag(buf[bin])
			if bin > 0 && bin < n/2 {
				p *= 2
			}
			power += p
		}
		powers[frame] = power
	}
	return coefficientOfVariation(powers)
}

// referenceFFT is an in-place iterative Cooley-Tukey FFT in complex128.
func referenceFFT(data []complex128) {
	n := len(data)
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

// referenceSignals builds the named test signals at sampleRate.
func referenceSignals(sampleRate int) map[string][]float32 {
	rng := rand.New(rand.NewPCG(1, 2))
	rate := float64(sampleRate)
	tone := make([]float32, referenceSamples)
	burst := make([]float32, referenceSamples)
	noise := make([]float32, referenceSamples)
	chirp := make([]float32, referenceSamples)
	am := make([]float32, referenceSamples)
	for i := range referenceSamples {
		t := float64(i) / rate
		tone[i] = quantize(0.01 * math.Sin(2*math.Pi*40000*t))
		if i >= referenceSamples/3 && i < 2*referenceSamples/3 {
			burst[i] = quantize(0.5 * math.Sin(2*math.Pi*45000*t))
		}
		noise[i] = quantize(0.02 * rng.NormFloat64())
		c := 0.02 * rng.NormFloat64()
		if i%20000 < 3000 {
			c += 0.3 * math.Sin(2*math.Pi*(30000+20000*t)*t)
		}
		chirp[i] = quantize(c)
		am[i] = quantize(0.05 * rng.NormFloat64() * (1 + 0.8*math.Sin(2*math.Pi*3*t)))
	}
	return map[string][]float32{
		"flat 40 kHz tone":    tone,
		"45 kHz burst":        burst,
		"white noise":         noise,
		"noise with chirps":   chirp,
		"amplitude-mod noise": am,
	}
}

func TestComputeUSFrameCV_MatchesFloat64FFTReference(t *testing.T) {
	t.Parallel()
	configs := []struct{ fft, hop int }{{8192, 4096}, {4096, 2048}, {8192, 8192}, {16384, 4096}}
	for _, sampleRate := range []int{192000, 256000, 384000} {
		signals := referenceSignals(sampleRate)
		for name, samples := range signals {
			for _, c := range configs {
				cfg := defaultFilterConfig()
				cfg.FFTSize, cfg.HopSize = c.fft, c.hop
				t.Run(fmt.Sprintf("%s/%dHz/%d-%d", name, sampleRate, c.fft, c.hop), func(t *testing.T) {
					t.Parallel()
					ref := referenceCV(samples, sampleRate, cfg, true)
					require.Greater(t, math.Abs(ref-cfg.CVThreshold), thresholdClearance,
						"the reference CV must not sit on the verdict threshold (ref=%g)", ref)

					cv, ok := ComputeUSFrameCV(samples, sampleRate, cfg)
					require.True(t, ok)
					assert.InDelta(t, ref, cv, ultrasonicCVAbsTol)
					assert.Equal(t, IsUnlikely(ref, cfg), IsUnlikely(cv, cfg), "cv=%g ref=%g", cv, ref)
				})
			}
		}
	}
}

// TestComputeUSFrameCV_DCOffsetDoesNotSwampUltrasonicBand covers a recording
// that is near-digital silence riding on a large DC offset: a constant level
// with 1 LSB clicks. A float32 FFT of the raw frame leaks roundoff from the DC
// bin into the ultrasonic bins that exceeds the clicks themselves, so the
// frame mean has to come off before the transform.
func TestComputeUSFrameCV_DCOffsetDoesNotSwampUltrasonicBand(t *testing.T) {
	t.Parallel()
	const (
		sampleRate = 192000
		numSamples = 81920
		clickEvery = 9973 // a prime, so clicks land at different positions per frame
	)
	for _, dcLSB := range []float64{20000, 30000} {
		t.Run(fmt.Sprintf("%.0f LSB", dcLSB), func(t *testing.T) {
			t.Parallel()
			samples := make([]float32, numSamples)
			for i := range samples {
				v := dcLSB
				if i%clickEvery == 0 {
					v++
				}
				samples[i] = float32(v / pcm16Scale)
			}
			cfg := defaultFilterConfig()

			ref := referenceCV(samples, sampleRate, cfg, true)
			require.Greater(t, math.Abs(ref-cfg.CVThreshold), thresholdClearance, "ref=%g", ref)

			cv, ok := ComputeUSFrameCV(samples, sampleRate, cfg)
			require.True(t, ok)
			assert.InDelta(t, ref, cv, ultrasonicCVAbsTol)
			assert.Equal(t, IsUnlikely(ref, cfg), IsUnlikely(cv, cfg), "cv=%g ref=%g", cv, ref)
		})
	}
}

func TestComputeUSFrameCV_RejectsInvalidSTFTConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fft, hop int
	}{
		{"hop above fft size", 8192, 16384},
		{"hop zero", 8192, 0},
		{"fft size not a power of two", 6000, 3000},
		{"fft size one", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultFilterConfig()
			cfg.FFTSize, cfg.HopSize = tt.fft, tt.hop
			cv, ok := ComputeUSFrameCV(make([]float32, referenceSamples), 256000, cfg)
			assert.False(t, ok)
			assert.InDelta(t, 0, cv, 0)
		})
	}
}
