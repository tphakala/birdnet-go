package soundlevel

import (
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-dsp/octave"
)

const (
	testSampleRate    = 48000
	sineAmplitude     = 0.5
	sineAtCentreTolDB = 0.05 // measured worst 0.015 dB (second period, 48 and 256 kHz)
	offBandRejectDB   = 20.0 // two octaves from a band centre a single biquad attenuates far more than this
	noiseSigma        = 0.1
	noiseBiasMinDB    = 0.5 // white noise minus rectangular band: measured 1.02 to 2.10 dB for bands >= 200 Hz at 48, 96, 256 kHz
	noiseBiasMaxDB    = 2.5
	noiseMinBandHz    = 200 // lower bands have too few degrees of freedom in 10 s for a tight bound
	noiseSeconds      = 10
	firstBandIndex    = -16 // 1/3-octave band index n of 25 Hz, where the centre is 1000*10^(n/10) Hz
	expectedBandCount = 30
	nanInjectTolDB    = 0.1
	hugeAmplitude     = 100.0
	dbEpsilon         = 1e-9
	lowestKey         = "25.0_Hz"
	refKey            = "1.0_kHz"
	highestKey        = "20.0_kHz"
)

// wantKeys is the wire contract: every band key of a full 30 band bank.
var wantKeys = []string{
	"25.0_Hz", "31.5_Hz", "40.0_Hz", "50.0_Hz", "63.0_Hz", "80.0_Hz", "100.0_Hz",
	"125.0_Hz", "160.0_Hz", "200.0_Hz", "250.0_Hz", "315.0_Hz", "400.0_Hz",
	"500.0_Hz", "630.0_Hz", "800.0_Hz", "1.0_kHz", "1.2_kHz", "1.6_kHz",
	"2.0_kHz", "2.5_kHz", "3.1_kHz", "4.0_kHz", "5.0_kHz", "6.3_kHz", "8.0_kHz",
	"10.0_kHz", "12.5_kHz", "16.0_kHz", "20.0_kHz",
}

// wantCenters are the nominal centre frequencies, in the order of wantKeys.
var wantCenters = []float64{
	25, 31.5, 40, 50, 63, 80, 100, 125, 160, 200, 250, 315, 400, 500, 630, 800,
	1000, 1250, 1600, 2000, 2500, 3150, 4000, 5000, 6300, 8000, 10000, 12500,
	16000, 20000,
}

// exactCentre returns the base-10 centre frequency of 1/3-octave band index n.
func exactCentre(n int) float64 { return 1000 * math.Pow(10, float64(n)/10) }

// bandCentre returns the exact centre of the i-th band of a full bank.
func bandCentre(i int) float64 { return exactCentre(firstBandIndex + i) }

// generateSine produces count samples of a sine wave starting at sample start.
func generateSine(freq, amplitude float64, sampleRate, start, count int) []float32 {
	samples := make([]float32, count)
	for i := range count {
		samples[i] = float32(amplitude * math.Sin(2*math.Pi*freq*float64(start+i)/float64(sampleRate)))
	}
	return samples
}

// generateNoise produces count samples of seeded Gaussian noise.
func generateNoise(sigma float64, count int, seed uint64) []float32 {
	rng := rand.New(rand.NewPCG(seed, seed+1))
	samples := make([]float32, count)
	for i := range samples {
		samples[i] = float32(sigma * rng.NormFloat64())
	}
	return samples
}

// collect feeds samples to p in one call and returns every emitted result.
func collect(p *Processor, samples []float32) []SoundLevelData {
	var out []SoundLevelData
	p.Process(samples, func(d SoundLevelData) { out = append(out, d) })
	return out
}

func newTestProcessor(t *testing.T, sampleRate, interval int) *Processor {
	t.Helper()
	p, err := NewProcessor("src", "name", sampleRate, interval)
	require.NoError(t, err)
	return p
}

func TestNewProcessor_BandKeysAndCentres(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{48000, 96000, 192000, 256000, 384000} {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			t.Parallel()
			p := newTestProcessor(t, rate, 1)
			assert.Equal(t, wantKeys, p.keys)
			assert.Equal(t, wantCenters, p.centers)
		})
	}
}

func TestNewProcessor_RejectsRateWithNoBand(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{0, -1, 59} {
		p, err := NewProcessor("src", "name", rate, 5)
		require.ErrorIs(t, err, octave.ErrInvalidConfig, "rate %d", rate)
		assert.Nil(t, p)
	}
}

func TestNewProcessor_IntervalClamping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		interval int
		want     int
	}{
		{"zero", 0, 1},
		{"negative", -3, 1},
		{"one", 1, 1},
		{"ten", 10, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := newTestProcessor(t, testSampleRate, tt.interval)
			assert.Equal(t, tt.want, p.interval)
		})
	}
}

func TestBandKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		freq     float64
		expected string
	}{
		{25, "25.0_Hz"},
		{31.5, "31.5_Hz"},
		{999.9, "999.9_Hz"},
		{1000, "1.0_kHz"},
		{1250, "1.2_kHz"},
		{3150, "3.1_kHz"},
		{12500, "12.5_kHz"},
		{20000, "20.0_kHz"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, bandKey(tt.freq))
		})
	}
}

func TestProcess_SineAtBandCentreReadsAnalyticLevel(t *testing.T) {
	t.Parallel()

	want := 10 * math.Log10(sineAmplitude*sineAmplitude/2)
	tests := []struct {
		rate int
		keys []string // nil means every band
	}{
		{testSampleRate, nil},
		{256000, []string{lowestKey, refKey, highestKey}},
	}
	for _, tt := range tests {
		ref := newTestProcessor(t, tt.rate, 1)
		for i, key := range ref.keys {
			if tt.keys != nil && !slices.Contains(tt.keys, key) {
				continue
			}
			t.Run(fmt.Sprintf("%dHz", tt.rate)+"/"+key, func(t *testing.T) {
				t.Parallel()
				p := newTestProcessor(t, tt.rate, 1)
				results := collect(p, generateSine(bandCentre(i), sineAmplitude, tt.rate, 0, 2*tt.rate))
				require.Len(t, results, 2)
				band := results[1].OctaveBands[key]
				assert.InDelta(t, want, band.Min, sineAtCentreTolDB)
				assert.InDelta(t, want, band.Max, sineAtCentreTolDB)
				assert.InDelta(t, want, band.Mean, sineAtCentreTolDB)

				if key == refKey {
					for _, off := range []string{"250.0_Hz", "4.0_kHz"} {
						assert.Less(t, results[1].OctaveBands[off].Mean, band.Mean-offBandRejectDB,
							"band %s must read far below the driven band", off)
					}
				}
			})
		}
	}
}

func TestProcess_WhiteNoiseReadsNoiseBandwidthAboveRectangularBand(t *testing.T) {
	t.Parallel()

	for _, rate := range []int{testSampleRate, 96000, 256000} {
		t.Run(fmt.Sprintf("%dHz", rate), func(t *testing.T) {
			t.Parallel()
			p := newTestProcessor(t, rate, noiseSeconds)
			require.Len(t, p.keys, expectedBandCount)
			results := collect(p, generateNoise(noiseSigma, noiseSeconds*rate, 1))
			require.Len(t, results, 1)

			for i, key := range p.keys {
				if p.centers[i] < noiseMinBandHz {
					continue
				}
				fc := bandCentre(i)
				width := fc * (math.Pow(10, 0.05) - math.Pow(10, -0.05))
				rect := 10 * math.Log10(noiseSigma*noiseSigma*width/(float64(rate)/2))
				bias := results[0].OctaveBands[key].Mean - rect
				assert.GreaterOrEqual(t, bias, noiseBiasMinDB, "band %s", key)
				assert.LessOrEqual(t, bias, noiseBiasMaxDB, "band %s", key)
			}
		})
	}
}

func TestLevelStats_ArithmeticMeanOfDB(t *testing.T) {
	t.Parallel()

	s := newLevelStats(1)
	for _, ms := range []float64{1e-2, 1e-4, 1e-6} {
		s.add([]float64{ms})
	}
	assert.Equal(t, 3, s.periods)
	assert.InDelta(t, -60.0, s.minDB[0], dbEpsilon)
	assert.InDelta(t, -20.0, s.maxDB[0], dbEpsilon)
	// The arithmetic mean of the dB values, not the energy mean (-24.26 dB).
	assert.InDelta(t, -40.0, s.mean(0), dbEpsilon)

	s.reset()
	assert.Zero(t, s.periods)
	s.add([]float64{1e-3})
	assert.InDelta(t, -30.0, s.minDB[0], dbEpsilon)
	assert.InDelta(t, -30.0, s.maxDB[0], dbEpsilon)
	assert.InDelta(t, -30.0, s.mean(0), dbEpsilon)
}

func TestLevelStats_MeanStaysWithinMinMax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		meanSquare float64
		periods    int
	}{
		{2e-7, 10},
		{3e-7, 6},
	}
	for _, tt := range tests {
		s := newLevelStats(1)
		for range tt.periods {
			s.add([]float64{tt.meanSquare})
		}
		assert.GreaterOrEqual(t, s.mean(0), s.minDB[0], "mean square %g over %d periods", tt.meanSquare, tt.periods)
		assert.LessOrEqual(t, s.mean(0), s.maxDB[0], "mean square %g over %d periods", tt.meanSquare, tt.periods)
	}
}

func TestProcess_ClampsToPublishableRange(t *testing.T) {
	t.Parallel()

	t.Run("loud", func(t *testing.T) {
		t.Parallel()
		p := newTestProcessor(t, testSampleRate, 2)
		results := collect(p, generateSine(1000, hugeAmplitude, testSampleRate, 0, 2*testSampleRate))
		require.Len(t, results, 1)
		band := results[0].OctaveBands[refKey]
		assert.InDelta(t, MaxLevelDB, band.Min, dbEpsilon)
		assert.InDelta(t, MaxLevelDB, band.Max, dbEpsilon)
		assert.InDelta(t, MaxLevelDB, band.Mean, dbEpsilon)
	})

	t.Run("silence", func(t *testing.T) {
		t.Parallel()
		p := newTestProcessor(t, testSampleRate, 2)
		results := collect(p, make([]float32, 2*testSampleRate))
		require.Len(t, results, 1)
		for key, band := range results[0].OctaveBands {
			assert.InDelta(t, MinLevelDB, band.Min, dbEpsilon, key)
			assert.InDelta(t, MinLevelDB, band.Max, dbEpsilon, key)
			assert.InDelta(t, MinLevelDB, band.Mean, dbEpsilon, key)
		}
	})

	t.Run("non-finite samples do not poison the filters", func(t *testing.T) {
		t.Parallel()
		clean := generateSine(1000, sineAmplitude, testSampleRate, 0, 2*testSampleRate)
		dirty := slices.Clone(clean)
		dirty[1000] = float32(math.NaN())
		dirty[testSampleRate/2] = float32(math.Inf(1))
		dirty[testSampleRate+10] = float32(math.Inf(-1))

		want := collect(newTestProcessor(t, testSampleRate, 2), clean)
		got := collect(newTestProcessor(t, testSampleRate, 2), dirty)
		require.Len(t, got, 1)
		band := got[0].OctaveBands[refKey]
		assert.False(t, math.IsNaN(band.Mean) || math.IsInf(band.Mean, 0))
		assert.Greater(t, band.Mean, MinLevelDB)
		assert.InDelta(t, want[0].OctaveBands[refKey].Mean, band.Mean, nanInjectTolDB)
	})
}

func TestProcess_ChunkingDoesNotChangeResults(t *testing.T) {
	t.Parallel()

	const (
		extraSamples  = 123
		wantResults   = 3
		maxChunkRates = 3
		randomRuns    = 5
		smallChunk    = 1
		midChunk      = 4800
	)
	samples := generateNoise(0.2, 7*testSampleRate+extraSamples, 3)

	run := func(next func() int) []SoundLevelData {
		p := newTestProcessor(t, testSampleRate, 2)
		var out []SoundLevelData
		for i := 0; i < len(samples); {
			n := min(next(), len(samples)-i)
			p.Process(samples[i:i+n], func(d SoundLevelData) { out = append(out, d) })
			i += n
		}
		for i := range out {
			out[i].Timestamp = time.Time{}
		}
		return out
	}

	want := run(func() int { return len(samples) })
	require.Len(t, want, wantResults)

	for seed := range uint64(randomRuns) {
		rng := rand.New(rand.NewPCG(seed, seed+100))
		got := run(func() int { return 1 + rng.IntN(maxChunkRates*testSampleRate) })
		require.Len(t, got, wantResults, "seed %d", seed)
		assert.Equal(t, want, got, "seed %d", seed)
	}

	sequence := []int{smallChunk, midChunk, 5 * testSampleRate / 2}
	var idx int
	got := run(func() int {
		n := sequence[idx%len(sequence)]
		idx++
		return n
	})
	require.Len(t, got, wantResults)
	assert.Equal(t, want, got)
}

func TestProcess_OneCallSpanningSeveralIntervalsEmitsEach(t *testing.T) {
	t.Parallel()

	p := newTestProcessor(t, testSampleRate, 1)
	noise := generateNoise(noiseSigma, 4*testSampleRate, 7)

	first := collect(p, noise[:3*testSampleRate+testSampleRate/2])
	require.Len(t, first, 3)
	second := collect(p, noise[3*testSampleRate+testSampleRate/2:])
	require.Len(t, second, 1)
	for _, d := range append(first, second...) {
		assert.Equal(t, 1, d.Duration)
	}
}

func TestProcess_ResultMetadata(t *testing.T) {
	t.Parallel()

	const interval = 3
	p, err := NewProcessor("src-1", "Source One", testSampleRate, interval)
	require.NoError(t, err)

	before := time.Now()
	results := collect(p, generateNoise(noiseSigma, interval*testSampleRate, 9))
	after := time.Now()

	require.Len(t, results, 1)
	d := results[0]
	assert.Equal(t, "src-1", d.Source)
	assert.Equal(t, "Source One", d.Name)
	assert.Equal(t, interval, d.Duration)
	assert.False(t, d.Timestamp.Before(before))
	assert.False(t, d.Timestamp.After(after))
	require.Len(t, d.OctaveBands, expectedBandCount)
	for key, band := range d.OctaveBands {
		assert.Equal(t, interval, band.SampleCount, key)
		assert.LessOrEqual(t, band.Min, band.Mean, key)
		assert.LessOrEqual(t, band.Mean, band.Max, key)
	}
}

func TestProcess_EmptyAndNilEmit(t *testing.T) {
	t.Parallel()

	p := newTestProcessor(t, testSampleRate, 1)
	calls := 0
	count := func(SoundLevelData) { calls++ }
	p.Process(nil, count)
	p.Process([]float32{}, count)
	assert.Zero(t, calls)

	noise := generateNoise(noiseSigma, 2*testSampleRate, 11)
	assert.NotPanics(t, func() { p.Process(noise[:testSampleRate], nil) })

	// The interval after the discarded one starts fresh: it holds exactly one
	// period, so nothing from the discarded second leaked into its statistics.
	got := collect(p, noise[testSampleRate:])
	require.Len(t, got, 1)
	for key, band := range got[0].OctaveBands {
		assert.Equal(t, 1, band.SampleCount, key)
	}
}

func BenchmarkProcessor_Process(b *testing.B) {
	for _, rate := range []int{testSampleRate, 256000} {
		b.Run(fmt.Sprintf("%dHz", rate), func(b *testing.B) {
			p, err := NewProcessor("src", "name", rate, noiseSeconds)
			require.NoError(b, err)
			chunk := generateNoise(noiseSigma, rate/10, 1)
			emit := func(SoundLevelData) {}
			b.ReportAllocs()
			for b.Loop() {
				p.Process(chunk, emit)
			}
		})
	}
}
