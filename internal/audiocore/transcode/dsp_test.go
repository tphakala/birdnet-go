package transcode

import (
	"bytes"
	"context"
	"math"
	"math/rand/v2"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-dsp/denoiser"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
)

const (
	// noiseOnlySeconds leads the denoise fixtures so the denoiser can learn the
	// noise floor from a quiet window.
	noiseOnlySeconds = 1.0
	noiseToneSeconds = 2.0
	noiseAmp         = 0.03
	noiseToneAmp     = 0.3
	noiseSeed        = 7

	// toneLevelToleranceDB is how far a denoised tone may drop.
	toneLevelToleranceDB = 3.0
	// lsbTolerance allows for float rounding in the chunked and one-shot paths.
	lsbTolerance = 1.0

	normalizeToleranceLU = 0.5
	// truePeakSlackDB absorbs the int16 quantization after the planned gain.
	truePeakSlackDB = 0.1
)

// noisePlusTone returns mono white noise of amplitude noiseLevel with a testToneHz
// tone of amplitude toneLevel added from frame toneStart on; both levels are
// fractions of full scale. The same seed gives the same noise.
func noisePlusTone(seed uint64, rate, frames, toneStart int, noiseLevel, toneLevel float64) []int16 {
	rng := rand.New(rand.NewPCG(seed, seed))
	out := make([]int16, frames)
	for i := range out {
		v := noiseLevel * rng.NormFloat64()
		if i >= toneStart {
			v += toneLevel * math.Sin(2*math.Pi*testToneHz*float64(i)/float64(rate))
		}
		out[i] = int16(math.Max(math.Min(math.Round(v*math.MaxInt16), math.MaxInt16), math.MinInt16))
	}
	return out
}

// noisyTone returns mono noise for noiseOnlySeconds followed by a tone in noise.
func noisyTone(rate int) []int16 {
	return noisePlusTone(noiseSeed, rate, int((noiseOnlySeconds+noiseToneSeconds)*float64(rate)), int(noiseOnlySeconds*float64(rate)), noiseAmp, noiseToneAmp)
}

// toneFit fits a sinusoid at freq to x by least squares and returns its
// amplitude and the residual RMS (what remains besides the tone).
func toneFit(x []float64, rate int, freq float64) (amp, residualRMS float64) {
	var sc, ss, sxs, sxc, scs float64
	for i, v := range x {
		s := math.Sin(2 * math.Pi * freq * float64(i) / float64(rate))
		c := math.Cos(2 * math.Pi * freq * float64(i) / float64(rate))
		ss += s * s
		sc += c * c
		scs += s * c
		sxs += v * s
		sxc += v * c
	}
	det := ss*sc - scs*scs
	a := (sxs*sc - sxc*scs) / det
	b := (sxc*ss - sxs*scs) / det
	var res float64
	for i, v := range x {
		s := math.Sin(2 * math.Pi * freq * float64(i) / float64(rate))
		c := math.Cos(2 * math.Pi * freq * float64(i) / float64(rate))
		d := v - a*s - b*c
		res += d * d
	}
	return math.Hypot(a, b), math.Sqrt(res / float64(len(x)))
}

func newPCM(samples []int16, rate, channels int) *pcmBuffer {
	return &pcmBuffer{Data: int16Bytes(samples), SampleRate: rate, Channels: channels}
}

func TestApplyFilters_NoFiltersLeaveBytesIdentical(t *testing.T) {
	t.Parallel()

	src := tone(testRate, 1, 0.5, testToneHz, testAmp)
	p := newPCM(src, testRate, 1)
	before := bytes.Clone(p.Data)
	require.NoError(t, applyFilters(t.Context(), p, Filters{}))
	assert.Equal(t, before, p.Data)
}

func TestFilters_Active(t *testing.T) {
	t.Parallel()

	assert.False(t, Filters{}.Active())
	assert.True(t, Filters{Denoise: "light"}.Active())
	assert.True(t, Filters{Normalize: true}.Active())
	assert.True(t, Filters{GainDB: -3}.Active())
}

func TestApplyFilters_UnknownDenoisePresetIsRejected(t *testing.T) {
	t.Parallel()

	for _, preset := range []string{"extreme", "LIGHT", "Medium", " heavy"} {
		p := newPCM(tone(testRate, 1, 0.2, testToneHz, testAmp), testRate, 1)
		err := applyFilters(t.Context(), p, Filters{Denoise: preset})
		require.ErrorIsf(t, err, ErrInvalidRequest, "preset %q must never fall back to a default strength", preset)
	}
}

// The chunked, in-place denoise must equal denoiser.Denoise on the whole clip, and
// the preset names must select the matching strengths.
func TestApplyFilters_ChunkedDenoiseMatchesOneShot(t *testing.T) {
	t.Parallel()

	presets := map[string]denoiser.Strength{"light": denoiser.Light, "medium": denoiser.Medium, "heavy": denoiser.Heavy}
	const oddExtraFrames = 777 // not a multiple of the hop
	for _, rate := range []int{48000, 44100} {
		for preset, strength := range presets {
			t.Run(preset+"_"+map[int]string{48000: "48k", 44100: "44k"}[rate], func(t *testing.T) {
				t.Parallel()
				src := noisyTone(rate)
				src = append(src, noisyTone(rate)[:oddExtraFrames]...)
				frames := len(src)
				require.Greater(t, frames, 2*chunkFrames, "the clip must span several chunks")

				p := newPCM(src, rate, 1)
				require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: preset}))

				x := make([]float32, frames)
				for i, s := range src {
					x[i] = float32(s) / pcm16Scale
				}
				ref, err := denoiser.Denoise(x, denoiser.Config{SampleRate: rate, Strength: strength})
				require.NoError(t, err)
				require.Len(t, ref, frames)

				got := bytesInt16(p.Data)
				require.Len(t, got, frames)
				for i := range ref {
					want := float64(floatToInt16(ref[i]))
					if math.Abs(float64(got[i])-want) > lsbTolerance {
						require.Failf(t, "chunked output diverges", "sample %d: got %d want %v", i, got[i], want)
					}
				}
			})
		}
	}
}

func TestApplyFilters_StereoDenoiseKeepsChannelsSeparate(t *testing.T) {
	t.Parallel()

	left := noisyTone(testRate)
	right := make([]int16, len(left)) // silent right channel
	inter := make([]int16, 2*len(left))
	for i := range left {
		inter[2*i] = left[i]
		inter[2*i+1] = right[i]
	}
	p := newPCM(inter, testRate, 2)
	require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: "medium"}))

	got := bytesInt16(p.Data)
	require.Len(t, got, len(inter))
	assert.Zero(t, peakAbs(bytesInt16(int16Bytes(channelInt16(got, 2, 1)))), "silence stays silent")
	assert.Greater(t, peakAbs(channelInt16(got, 2, 0)), 1000, "the left channel keeps its tone")
}

func channelInt16(samples []int16, channels, ch int) []int16 {
	out := make([]int16, len(samples)/channels)
	for i := range out {
		out[i] = samples[i*channels+ch]
	}
	return out
}

func TestApplyFilters_DenoiseReducesNoiseMonotonically(t *testing.T) {
	t.Parallel()

	src := noisyTone(testRate)
	lead := int(noiseOnlySeconds * testRate)
	measure := func(samples []int16) (amp, residual float64) {
		return toneFit(channelOf(samples[lead:], 1, 0), testRate, testToneHz)
	}
	origAmp, origResidual := measure(src)

	residuals := map[string]float64{}
	for _, preset := range []string{"light", "medium", "heavy"} {
		p := newPCM(src, testRate, 1)
		require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: preset}))
		amp, residual := measure(bytesInt16(p.Data))
		residuals[preset] = residual
		drop := 20 * math.Log10(origAmp/amp)
		assert.LessOrEqualf(t, drop, toneLevelToleranceDB, "%s keeps the tone within %v dB", preset, toneLevelToleranceDB)
		assert.Lessf(t, residual, origResidual, "%s removes noise", preset)
	}
	assert.Greater(t, residuals["light"], residuals["medium"], "medium removes more noise than light")
	assert.Greater(t, residuals["medium"], residuals["heavy"], "heavy removes more noise than medium")
}

func TestApplyFilters_DenoiseShorterThanOneFrame(t *testing.T) {
	t.Parallel()

	const tinyFrames = 100
	for _, preset := range []string{"light", "medium", "heavy"} {
		src := tone(testRate, 1, float64(tinyFrames)/testRate, testToneHz, testAmp)
		require.Len(t, src, tinyFrames)
		p := newPCM(src, testRate, 1)
		require.NotPanics(t, func() {
			require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: preset}))
		})
		assert.Equal(t, tinyFrames, p.Frames())
	}
}

func TestApplyFilters_DenoiseAtTheSourceRateBounds(t *testing.T) {
	t.Parallel()

	const boundSeconds = 0.5
	for _, rate := range []int{minSourceSampleRate, maxSourceSampleRate} {
		src := tone(rate, 1, boundSeconds, testToneHz, testAmp)
		p := newPCM(src, rate, 1)
		require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: "heavy", Normalize: true}))
		assert.Equal(t, len(src), p.Frames(), "rate %d", rate)
	}
}

func TestApplyFilters_DenoiseCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := newPCM(noisyTone(testRate), testRate, 1)
	require.ErrorIs(t, applyFilters(ctx, p, Filters{Denoise: "light"}), context.Canceled)
}

// allocSeconds is the length of the clip for the allocation test: long enough
// that one channel of floats dwarfs the denoiser's own working set.
const allocSeconds = 60

// allocatedBy returns the bytes allocated process-wide while fn runs.
func allocatedBy(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// This test reads the process-wide allocation counter, so it must not run in
// parallel with the tests that allocate buffers (serial tests finish before the
// paused parallel ones resume, as in TestDecode_HeaderClaimingHugeLengthDoesNotDriveAllocation).
func TestDenoisePCM_AllocatesLessThanOneChannelOfFloats(t *testing.T) {
	frames := allocSeconds * testRate
	src := make([]int16, 0, frames)
	for len(src) < frames {
		src = append(src, noisyTone(testRate)...)
	}
	src = src[:frames]
	channelOfFloats := uint64(frames) * 4 //nolint:gosec // G115: a small positive test constant

	streaming := newPCM(src, testRate, 1)
	streamed := allocatedBy(func() {
		require.NoError(t, denoisePCM(t.Context(), streaming, "heavy"))
	})
	t.Logf("channel of floats %d bytes; streaming denoise allocated %d", channelOfFloats, streamed)

	assert.Less(t, streamed, channelOfFloats/2, "the streaming denoise must not hold a whole channel of floats")
}

// lufsOf measures integrated loudness and true peak of mono 16-bit PCM.
func lufsOf(t *testing.T, p *pcmBuffer) audionorm.Measurement {
	t.Helper()
	m, err := audionorm.MeasureInt16Bytes(p.Data, p.SampleRate, p.Channels)
	require.NoError(t, err)
	return m
}

func TestApplyFilters_NormalizeHitsTargetUnderCeiling(t *testing.T) {
	t.Parallel()

	const quietAmp = 0.01
	p := newPCM(tone(testRate, 1, 6, testToneHz, quietAmp), testRate, 1)
	before := lufsOf(t, p)
	require.Less(t, before.IntegratedLUFS, normalizeTargetLUFS-10, "fixture must start well below the target")

	require.NoError(t, applyFilters(t.Context(), p, Filters{Normalize: true}))
	after := lufsOf(t, p)
	assert.InDelta(t, normalizeTargetLUFS, after.IntegratedLUFS, normalizeToleranceLU)
	assert.LessOrEqual(t, after.TruePeakDBTP, normalizeTruePeakDBTP+truePeakSlackDB)
}

func TestApplyFilters_NormalizeLimitedByTruePeak(t *testing.T) {
	t.Parallel()

	// Sparse loud clicks in near-silence: loudness is low but the peak is high, so
	// the ceiling, not the target, bounds the gain.
	src := make([]int16, 6*testRate)
	for i := 0; i < len(src); i += testRate / 2 {
		src[i] = 30000
	}
	p := newPCM(src, testRate, 1)
	require.NoError(t, applyFilters(t.Context(), p, Filters{Normalize: true}))
	assert.LessOrEqual(t, lufsOf(t, p).TruePeakDBTP, normalizeTruePeakDBTP+truePeakSlackDB)
}

func TestApplyFilters_NormalizeSubGateMatchesExport(t *testing.T) {
	t.Parallel()

	const subGateAmp = 0.0001 // about -80 dBFS
	src := tone(testRate, 1, 5, testToneHz, subGateAmp)
	p := newPCM(src, testRate, 1)
	orig := bytes.Clone(p.Data)

	plan, err := audionorm.PlanGainWithGateFallbackInt16Bytes(orig, audionorm.Options{
		SampleRate: testRate, Channels: 1, TargetLUFS: normalizeTargetLUFS, TruePeakDBTP: normalizeTruePeakDBTP,
	}, audionorm.MaxNormalizeGainDB)
	require.NoError(t, err)
	require.True(t, plan.GateLifted, "the fixture must sit under the absolute gate")
	require.NotZero(t, plan.GainDB)

	require.NoError(t, applyFilters(t.Context(), p, Filters{Normalize: true}))
	assert.Equal(t, pcmgain.Applied(orig, plan.GainDB), p.Data, "the same plan the clip export uses")
}

func TestApplyFilters_OrderNormalizeThenGain(t *testing.T) {
	t.Parallel()

	const extraGainDB = 6.0
	p := newPCM(tone(testRate, 1, 6, testToneHz, 0.01), testRate, 1)
	require.NoError(t, applyFilters(t.Context(), p, Filters{Normalize: true, GainDB: extraGainDB}))
	assert.InDelta(t, normalizeTargetLUFS+extraGainDB, lufsOf(t, p).IntegratedLUFS, normalizeToleranceLU,
		"gain is added on top of the normalized level")
}

func TestApplyFilters_GainScalesAndSaturates(t *testing.T) {
	t.Parallel()

	t.Run("minus 6 dB halves the amplitude", func(t *testing.T) {
		t.Parallel()
		src := tone(testRate, 1, 0.5, testToneHz, testAmp)
		p := newPCM(src, testRate, 1)
		require.NoError(t, applyFilters(t.Context(), p, Filters{GainDB: -6.0206}))
		assert.InDelta(t, peakAbs(src)/2, peakAbs(bytesInt16(p.Data)), 2)
	})

	t.Run("a hot gain clamps instead of wrapping", func(t *testing.T) {
		t.Parallel()
		src := tone(testRate, 1, 0.5, testToneHz, 0.9)
		p := newPCM(src, testRate, 1)
		require.NoError(t, applyFilters(t.Context(), p, Filters{GainDB: 20}))
		got := bytesInt16(p.Data)
		assert.Equal(t, math.MaxInt16, slicesMax(got))
		assert.Equal(t, math.MinInt16, slicesMin(got))
		// A wrapped sample would flip sign against its source neighbor.
		for i, s := range src {
			if s > 0 {
				require.GreaterOrEqual(t, got[i], int16(0), "sample %d wrapped", i)
			}
		}
	})
}

func slicesMax(s []int16) int {
	m := math.MinInt
	for _, v := range s {
		m = max(m, int(v))
	}
	return m
}

func slicesMin(s []int16) int {
	m := math.MaxInt
	for _, v := range s {
		m = min(m, int(v))
	}
	return m
}

// Normalization measures the denoised samples: the loudness after both filters
// must land on the target, which it cannot if the denoiser runs after the gain
// was planned.
func TestApplyFilters_NormalizeMeasuresTheDenoisedSamples(t *testing.T) {
	t.Parallel()

	const (
		loudNoiseAmp = 0.1
		quietToneAmp = 0.05
	)
	rng := rand.New(rand.NewPCG(noiseSeed, noiseSeed))
	lead := int(noiseOnlySeconds * testRate)
	total := lead + int(noiseToneSeconds*testRate)
	src := make([]int16, total)
	for i := range src {
		v := loudNoiseAmp * rng.NormFloat64()
		if i >= lead {
			v += quietToneAmp * math.Sin(2*math.Pi*testToneHz*float64(i)/testRate)
		}
		src[i] = floatToInt16(float32(v))
	}

	p := newPCM(src, testRate, 1)
	require.NoError(t, applyFilters(t.Context(), p, Filters{Denoise: "heavy", Normalize: true}))
	assert.InDelta(t, normalizeTargetLUFS, lufsOf(t, p).IntegratedLUFS, normalizeToleranceLU)
}
