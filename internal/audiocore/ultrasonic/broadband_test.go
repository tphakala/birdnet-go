package ultrasonic

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dsppcm "github.com/tphakala/go-audio-dsp/pcm"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// Seeds and amplitudes of the synthetic windows. Everything is deterministic.
	noiseSigma       = 0.002
	noiseSeed        = 11
	clickSeed        = 21
	guardSeed        = 31
	clickAmplitude   = 0.02
	clickLengthSec   = 0.0015
	clickEverySec    = 0.060
	burstAmplitude   = 0.02
	burstLengthSec   = 0.004
	burstHz          = 45000
	burstEverySec    = 0.060
	weakBurstAmp     = 0.006
	weakBurstHz      = 30000
	weakBurstLenSec  = 0.006
	weakBurstEvery   = 0.090
	guardTones       = 48
	guardAmplitude   = 0.004
	guardDepth       = 0.3
	guardEnvelopeHz  = 23
	guardLowHz       = 11000
	guardSpanHz      = 6000
	batRate          = 256000
	batSamples       = 144000
	bsgBatRate       = 384000
	bsgBatSamples    = 393472
	silenceBurstAmp  = 0.5
	keptShareOfRaw   = 0.9
	clickRawCVFloor  = 0.5
	lowerBoundMargin = 0.5
	// A loud band without the slow envelope, low in the guard band. It keeps the
	// guard band median high, as rain does.
	loudGuardSeed      = 41
	loudGuardLowHz     = 12000
	loudGuardSpanHz    = 500
	loudGuardAmplitude = 0.05
	// One-sample impulses: the flattest broadband event there is.
	impulseAmplitude = 0.3
	// Recordings as signed 16 bit little endian PCM at 256 kHz: one window of
	// rain, and a weak call with 4096 samples more than a window, so that it can
	// be run at several window starts.
	rainFixture     = "rain_256k_s16le.pcm"
	weakCallFixture = "weak_bat_call_256k_s16le.pcm"
	// A split whose continuity range would end above the Nyquist frequency of
	// the bat rate, past the last bin of the spectrum (128.64 kHz against 128 kHz).
	highSplitHz = 107200
	// A split whose continuity range would end 40 Hz above the Nyquist frequency
	// of the bat rate, inside the last bin, and a loud band low in its guard band.
	nearNyquistSplitHz   = 106700
	nearNyquistGuardLoHz = 64500
	// Bytes per sample of the recordings.
	bytesPerPCM16Sample = 2
	// At this FFT size the continuity bands are narrower than one bin.
	narrowContinuityFFT = 1024
)

// weakBurstSeedOffsets are added to the noise and tone seeds of the weak
// bursts window. On every offset but the first, a continuity rule of six bands
// from 0.7 of the split to the split, each counted as raised as soon as it is
// above its median, takes more than a tenth of the CV.
var weakBurstSeedOffsets = []uint64{0, 38, 45, 53, 56}

// weakCallStarts are window starts into the weak call recording. They put the
// calls at different places in the sub-frame grid. At 109 and 1131 a rule with
// six wider bands over the same range takes the call; at 740 and the later
// starts a rule without a margin does.
var weakCallStarts = []int{0, 109, 740, 1131, 1258, 2294, 3293}

// modelWindows are the analysis window layouts of the two bat models.
var modelWindows = []struct {
	name    string
	rate, n int
}{
	{"Bat", batRate, batSamples},
	{"BSG-BAT", bsgBatRate, bsgBatSamples},
}

// whiteNoise returns a seeded Gaussian floor.
func whiteNoise(n int, sigma float64, seed uint64) []float64 {
	rng := rand.New(rand.NewPCG(seed, seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = sigma * rng.NormFloat64()
	}
	return out
}

// hann returns the raised-cosine envelope value at sample i of n.
func hann(i, n int) float64 {
	return 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
}

// addClicks adds short Hann-shaped white noise bursts, which have energy from DC to Nyquist.
func addClicks(x []float64, rate int, every, lenSec, amp float64, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, seed+1))
	clickLen := int(lenSec * float64(rate))
	step := int(every * float64(rate))
	for start := step / 2; start+clickLen < len(x); start += step {
		for i := range clickLen {
			x[start+i] += amp * hann(i, clickLen) * rng.NormFloat64()
		}
	}
}

// addToneBursts adds Hann-shaped tone bursts at hz.
func addToneBursts(x []float64, rate int, hz, every, lenSec, amp float64) {
	burstLen := int(lenSec * float64(rate))
	step := int(every * float64(rate))
	for start := step / 2; start+burstLen < len(x); start += step {
		for i := range burstLen {
			x[start+i] += amp * hann(i, burstLen) * math.Sin(2*math.Pi*hz*float64(i)/float64(rate))
		}
	}
}

// addToneBand adds tones between lowHz and lowHz+spanHz whose summed power
// swings by depth with a slow envelope; depth 0 leaves the envelope out.
func addToneBand(x []float64, rate int, lowHz, spanHz, amp, depth float64, seed uint64) {
	rng := rand.New(rand.NewPCG(seed, seed+2))
	freq := make([]float64, guardTones)
	phase := make([]float64, guardTones)
	for k := range guardTones {
		freq[k] = lowHz + spanHz*rng.Float64()
		phase[k] = 2 * math.Pi * rng.Float64()
	}
	for i := range x {
		t := float64(i) / float64(rate)
		var s float64
		for k := range guardTones {
			s += math.Sin(2*math.Pi*freq[k]*t + phase[k])
		}
		x[i] += amp * (1 + depth*math.Sin(2*math.Pi*guardEnvelopeHz*t)) * s / math.Sqrt(guardTones)
	}
}

// addImpulses adds one-sample impulses, whose spectrum is flat from DC to Nyquist.
func addImpulses(x []float64, rate int, every, amp float64) {
	step := int(every * float64(rate))
	for i := step / 2; i < len(x); i += step {
		x[i] += amp
	}
}

// readPCM16 loads a recorded window from testdata.
func readPCM16(t *testing.T, name string) []float32 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	out := make([]float32, len(raw)/bytesPerPCM16Sample)
	_, err = dsppcm.BytesToFloat32(out, raw)
	require.NoError(t, err)
	return out
}

func quantizeAll(x []float64) []float32 {
	out := make([]float32, len(x))
	for i, v := range x {
		out[i] = quantize(v)
	}
	return out
}

// clickWindow is the broadband click train over a low noise floor.
func clickWindow(rate, n int) []float32 {
	x := whiteNoise(n, noiseSigma, noiseSeed)
	addClicks(x, rate, clickEverySec, clickLengthSec, clickAmplitude, clickSeed)
	return quantizeAll(x)
}

func TestBroadbandAdjustedCV_ClickTrainIsLowered(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	for _, tt := range modelWindows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			samples := clickWindow(tt.rate, tt.n)
			cv, ok := ComputeUSFrameCV(samples, tt.rate, cfg)
			require.True(t, ok)
			require.Greater(t, cv, clickRawCVFloor, "the raw CV must pass the current check")

			got := BroadbandAdjustedCV(samples, tt.rate, cfg, cv)
			assert.Less(t, got, cfg.CVThreshold*lowerBoundMargin)
			assert.True(t, IsUnlikely(got, cfg))
		})
	}
}

// A loud band low in the guard band keeps its median high, so the impulses do
// not stand out there; they still raise every band up to the split.
func TestBroadbandAdjustedCV_ImpulsesOverLoudGuardBandAreLowered(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	for _, tt := range modelWindows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			x := whiteNoise(tt.n, noiseSigma, noiseSeed)
			addToneBand(x, tt.rate, loudGuardLowHz, loudGuardSpanHz, loudGuardAmplitude, 0, loudGuardSeed)
			addImpulses(x, tt.rate, clickEverySec, impulseAmplitude)
			samples := quantizeAll(x)
			cv, ok := ComputeUSFrameCV(samples, tt.rate, cfg)
			require.True(t, ok)
			require.False(t, IsUnlikely(cv, cfg), "the raw CV must pass the current check")

			got := BroadbandAdjustedCV(samples, tt.rate, cfg, cv)
			assert.Less(t, got, cfg.CVThreshold*lowerBoundMargin)
		})
	}
}

// Rain recorded at 192 kHz on a home station and resampled to the bat model
// rate: its ticks run from about 14 kHz through the split and the rain keeps
// the guard band median high, so the guard band rule alone leaves the CV at
// 0.35.
func TestBroadbandAdjustedCV_RecordedRainIsLowered(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	samples := readPCM16(t, rainFixture)
	cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg), "the raw CV must pass the current check")

	assert.True(t, IsUnlikely(BroadbandAdjustedCV(samples, batRate, cfg, cv), cfg))
}

// Two faint Eptesicus nilssonii calls near 30 kHz from the same station. The
// calls come with a slight rise below the split, and the window is close to
// the threshold, so nothing may be taken from it wherever the window starts.
func TestBroadbandAdjustedCV_RecordedWeakCallStaysActive(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	recording := readPCM16(t, weakCallFixture)
	for _, start := range weakCallStarts {
		t.Run("start "+strconv.Itoa(start), func(t *testing.T) {
			samples := recording[start : start+batSamples]
			cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
			require.True(t, ok)
			require.False(t, IsUnlikely(cv, cfg))

			assert.InDelta(t, cv, BroadbandAdjustedCV(samples, batRate, cfg, cv), 0)
		})
	}
}

// The continuity range of a split close to Nyquist would end above it. The
// continuity rule is then off, and the guard band rule must keep working.
func TestBroadbandAdjustedCV_ContinuityRangeAboveNyquistKeepsGuardRule(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	cfg.FrequencySplitHz = highSplitHz
	samples := clickWindow(batRate, batSamples)
	cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg))

	assert.True(t, IsUnlikely(BroadbandAdjustedCV(samples, batRate, cfg, cv), cfg))
}

// A range that ends above Nyquist by less than one bin still fits the
// spectrum in whole bins, but the rule is off for it: impulses that the guard
// band rule cannot see keep the window active.
func TestBroadbandAdjustedCV_ContinuityRangeJustAboveNyquistIsOff(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	cfg.FrequencySplitHz = nearNyquistSplitHz
	x := whiteNoise(batSamples, noiseSigma, noiseSeed)
	addToneBand(x, batRate, nearNyquistGuardLoHz, loudGuardSpanHz, loudGuardAmplitude, 0, loudGuardSeed)
	addImpulses(x, batRate, clickEverySec, impulseAmplitude)
	samples := quantizeAll(x)
	cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg))

	assert.False(t, IsUnlikely(BroadbandAdjustedCV(samples, batRate, cfg, cv), cfg))
}

// With bands narrower than one bin the continuity rule is off: the guard band
// rule keeps working, and band limited bursts keep their CV, which they would
// lose to a continuity rule left on without bands.
func TestBroadbandAdjustedCV_NarrowContinuityRangeKeepsGuardRule(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	cfg.FFTSize, cfg.HopSize = narrowContinuityFFT, narrowContinuityFFT/2
	samples := clickWindow(batRate, batSamples)
	cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg))
	assert.True(t, IsUnlikely(BroadbandAdjustedCV(samples, batRate, cfg, cv), cfg))

	x := whiteNoise(batSamples, noiseSigma, noiseSeed)
	addToneBursts(x, batRate, burstHz, burstEverySec, burstLengthSec, burstAmplitude)
	bursts := quantizeAll(x)
	cv, ok = ComputeUSFrameCV(bursts, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg))
	assert.InDelta(t, cv, BroadbandAdjustedCV(bursts, batRate, cfg, cv), 0)
}

func TestBroadbandAdjustedCV_BandLimitedBurstsKeepCV(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	x := whiteNoise(batSamples, noiseSigma, noiseSeed)
	addToneBursts(x, batRate, burstHz, burstEverySec, burstLengthSec, burstAmplitude)
	samples := quantizeAll(x)
	cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
	require.True(t, ok)
	require.False(t, IsUnlikely(cv, cfg))

	assert.InDelta(t, cv, BroadbandAdjustedCV(samples, batRate, cfg, cv), 0)
}

func TestBroadbandAdjustedCV_WeakBurstsOverGuardFluctuationStayActive(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	for _, offset := range weakBurstSeedOffsets {
		t.Run("seed offset "+strconv.FormatUint(offset, 10), func(t *testing.T) {
			x := whiteNoise(batSamples, noiseSigma, noiseSeed+offset)
			addToneBand(x, batRate, guardLowHz, guardSpanHz, guardAmplitude, guardDepth, guardSeed+offset)
			addToneBursts(x, batRate, weakBurstHz, weakBurstEvery, weakBurstLenSec, weakBurstAmp)
			samples := quantizeAll(x)
			cv, ok := ComputeUSFrameCV(samples, batRate, cfg)
			require.True(t, ok)
			require.False(t, IsUnlikely(cv, cfg))

			got := BroadbandAdjustedCV(samples, batRate, cfg, cv)
			assert.GreaterOrEqual(t, got, keptShareOfRaw*cv)
			assert.False(t, IsUnlikely(got, cfg))
		})
	}
}

func TestBroadbandAdjustedCV_FlatWindowIsNotExamined(t *testing.T) {
	t.Parallel()
	cfg := defaultFilterConfig()
	samples := clickWindow(batRate, batSamples)

	flat := math.Nextafter(cfg.CVThreshold, 0)
	assert.InDelta(t, flat, BroadbandAdjustedCV(samples, batRate, cfg, flat), 0)
	assert.Less(t, BroadbandAdjustedCV(samples, batRate, cfg, cfg.CVThreshold), cfg.CVThreshold)
}

func TestBroadbandAdjustedCV_DegenerateSetupKeepsCV(t *testing.T) {
	t.Parallel()
	const inputCV = 1.0
	withConfig := func(fft, split int) conf.UltrasonicFilterConfig {
		cfg := defaultFilterConfig()
		cfg.FFTSize, cfg.HopSize, cfg.FrequencySplitHz = fft, fft/2, split
		return cfg
	}
	tests := []struct {
		name    string
		cfg     conf.UltrasonicFilterConfig
		samples int
	}{
		{"fft 8 has no sub-frame hop", withConfig(8, 20000), batSamples},
		{"fft 128 guard starts at the DC bin", withConfig(128, 20000), batSamples},
		{"split 500 Hz guard is narrower than one bin", withConfig(8192, 500), batSamples},
		{"fewer than two sub-frames", withConfig(8192, 20000), 1535},
		{"split above the band", withConfig(8192, batRate*5/8), batSamples},
		{"invalid STFT size", withConfig(6000, 20000), batSamples},
	}
	click := clickWindow(batRate, batSamples)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, inputCV, BroadbandAdjustedCV(click[:tt.samples], batRate, tt.cfg, inputCV), 0)
		})
	}

	// Without a rule for a zero guard median, digital silence with one
	// ultrasonic burst would be treated as broadband and tagged.
	t.Run("zero guard median", func(t *testing.T) {
		t.Parallel()
		cfg := defaultFilterConfig()
		silence := make([]float32, batSamples)
		for i := batSamples / 3; i < 2*batSamples/3; i++ {
			silence[i] = float32(silenceBurstAmp * math.Sin(2*math.Pi*burstHz*float64(i)/batRate))
		}
		cv, ok := ComputeUSFrameCV(silence, batRate, cfg)
		require.True(t, ok)
		assert.InDelta(t, cv, BroadbandAdjustedCV(silence, batRate, cfg, cv), 0)
	})
}

func BenchmarkBroadbandAdjustedCV(b *testing.B) {
	cfg := defaultFilterConfig()
	samples := clickWindow(batRate, batSamples)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		BroadbandAdjustedCV(samples, batRate, cfg, 1.0)
	}
}
