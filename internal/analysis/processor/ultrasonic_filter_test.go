package processor

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

const (
	ultrasonicTestSourceID   = "ultrasonic-test-source"
	ultrasonicTestConnection = "hw:9,0"
	ultrasonicTestRate       = 256000
	ultrasonicTestSamples    = 144000
	ultrasonicTestToneHz     = 40000.0
	ultrasonicTestAmplitude  = 3000.0

	// The rate cases cover the filter's 20 kHz split: the analysis window is
	// always at the model's rate, whatever the source rate.
	ultrasonicBurstToneHz   = 25000.0 // above the split, below the 26.7 kHz a 256 kHz window gives at a 192 kHz rate
	ultrasonicLowBurstHz    = 15000.0 // below the split, above the 13.3 kHz a 256 kHz window gives at a 384 kHz rate
	ultrasonicSteadyToneHz  = 30000.0
	ultrasonicSteadyAmp     = 300.0
	ultrasonicNoiseAmp      = 20.0
	ultrasonicWindowSeconds = 3
	ultrasonicEnvelopeHz    = 10.0 // slow envelope: its sidebands stay far from the 20 kHz split
	ultrasonicNoiseSeed     = 12345
	ultrasonicLCGMul        = 1664525
	ultrasonicLCGInc        = 1013904223
	ultrasonicNoiseBits     = 16
)

// flatUltrasonicToneS16LE returns PCM16 of a constant 40 kHz tone: flat
// ultrasonic energy, which the filter tags as unlikely.
func flatUltrasonicToneS16LE() []byte {
	pcm := make([]byte, ultrasonicTestSamples*bytesPerPCM16Sample)
	for i := range ultrasonicTestSamples {
		v := ultrasonicTestAmplitude * math.Sin(2*math.Pi*ultrasonicTestToneHz*float64(i)/ultrasonicTestRate)
		binary.LittleEndian.PutUint16(pcm[i*bytesPerPCM16Sample:], uint16(int16(math.Round(v)))) //nolint:gosec // G115: intentional int16 to uint16 bit reinterpretation for PCM
	}
	return pcm
}

// TestApplyUltrasonicFilter_OddLengthPCMIsNotFiltered verifies that PCM with a
// half sample at the end is skipped instead of being filtered on zeroed samples,
// while the same audio with an even length is tagged unlikely.
func TestApplyUltrasonicFilter_OddLengthPCMIsNotFiltered(t *testing.T) {
	p, settings := newUltrasonicFilterFixture(t)
	run := func(pcm []byte) bool { return runUltrasonicFilter(p, settings, pcm) }

	pcm := flatUltrasonicToneS16LE()
	require.True(t, run(pcm), "the aligned flat tone must be tagged unlikely")

	assert.False(t, run(append(pcm, 0x01)), "PCM ending in a half sample must not be filtered")
}

// TestApplyUltrasonicFilter_InvalidSTFTSettingsAreNotFiltered verifies that an
// STFT configuration the filter refuses (a hop above the FFT size, or a split of
// 0 Hz that would sum every bin) tags nothing.
func TestApplyUltrasonicFilter_InvalidSTFTSettingsAreNotFiltered(t *testing.T) {
	p, settings := newUltrasonicFilterFixture(t)
	pcm := flatUltrasonicToneS16LE()
	require.True(t, runUltrasonicFilter(p, settings, pcm), "the valid configuration must tag the flat tone")

	settings.Bat.UltrasonicFilter.HopSize = settings.Bat.UltrasonicFilter.FFTSize * 2
	assert.False(t, runUltrasonicFilter(p, settings, pcm), "hop above the FFT size must not filter")

	settings.Bat.UltrasonicFilter.HopSize = conf.DefaultUltrasonicHopSize
	settings.Bat.UltrasonicFilter.FrequencySplitHz = 0
	assert.False(t, runUltrasonicFilter(p, settings, pcm), "a 0 Hz split must not filter")
}

// TestApplyUltrasonicFilter_SplitAtNyquistIsNotFiltered verifies that a source
// whose rate leaves no band above the split is skipped.
func TestApplyUltrasonicFilter_SplitAtNyquistIsNotFiltered(t *testing.T) {
	p, settings := newUltrasonicFilterFixture(t)
	pcm := flatUltrasonicToneS16LE()
	require.True(t, runUltrasonicFilter(p, settings, pcm), "the default split must tag the flat tone")

	settings.Bat.UltrasonicFilter.FrequencySplitHz = ultrasonicTestRate / 2
	assert.False(t, runUltrasonicFilter(p, settings, pcm), "a split at Nyquist leaves no band")
}

// newUltrasonicFilterFixture returns a Processor whose registry knows a 256 kHz
// source and settings with the default ultrasonic filter enabled.
func newUltrasonicFilterFixture(t *testing.T) (*Processor, *conf.Settings) {
	t.Helper()
	return newUltrasonicFilterFixtureAt(t, ultrasonicTestRate)
}

// newUltrasonicFilterFixtureAt is newUltrasonicFilterFixture with a registered
// source rate of sourceRate.
func newUltrasonicFilterFixtureAt(t *testing.T, sourceRate int) (*Processor, *conf.Settings) {
	t.Helper()
	registry := audiocore.NewSourceRegistry(audiocore.GetLogger())
	_, err := registry.Register(&audiocore.SourceConfig{
		ID:               ultrasonicTestSourceID,
		ConnectionString: ultrasonicTestConnection,
		DisplayName:      "Ultrasonic test",
		Type:             audiocore.SourceTypeAudioCard,
		SampleRate:       sourceRate,
	})
	require.NoError(t, err)
	p := &Processor{}
	p.SetRegistry(registry)

	settings := &conf.Settings{}
	settings.Bat.UltrasonicFilter = conf.UltrasonicFilterConfig{
		Enabled:          true,
		CVThreshold:      0.15,
		FFTSize:          conf.DefaultUltrasonicFFTSize,
		HopSize:          conf.DefaultUltrasonicHopSize,
		FrequencySplitHz: conf.DefaultUltrasonicSplitHz,
	}

	return p, settings
}

// runUltrasonicFilter applies the filter to pcm and reports whether the
// detection was tagged unlikely.
func runUltrasonicFilter(p *Processor, settings *conf.Settings, pcm []byte) bool {
	return runUltrasonicFilterForModel(p, settings, classifier.RegistryIDBat, pcm)
}

// runUltrasonicFilterForModel applies the filter to the analysis window pcm of
// the model modelID and reports whether the detection was tagged unlikely.
func runUltrasonicFilterForModel(p *Processor, settings *conf.Settings, modelID string, pcm []byte) bool {
	item := classifier.Results{
		PCMdata: pcm,
		ModelID: modelID,
		Source:  datastore.AudioSource{ID: ultrasonicTestConnection},
	}
	detections := []Detections{{Result: detection.Result{}}}
	p.applyUltrasonicFilter(settings, item, detections)
	return detections[0].Result.Unlikely
}

// synthUltrasonicWindowS16LE returns seconds of PCM16 at rate: burst is a tone at
// burstHz with a slow raised-cosine envelope, steadyAmp a constant 30 kHz tone, and
// a little deterministic noise keeps the quiet band from being digital silence.
func synthUltrasonicWindowS16LE(rate int, burstHz, burstAmp, steadyAmp float64) []byte {
	n := rate * ultrasonicWindowSeconds
	pcm := make([]byte, n*bytesPerPCM16Sample)
	state := uint32(ultrasonicNoiseSeed)
	for i := range n {
		t := float64(i) / float64(rate)
		envelope := 0.5 * (1 + math.Sin(2*math.Pi*ultrasonicEnvelopeHz*t))
		state = state*ultrasonicLCGMul + ultrasonicLCGInc
		noise := (float64(state>>ultrasonicNoiseBits)/math.MaxUint16 - 0.5) * 2 * ultrasonicNoiseAmp
		v := burstAmp*envelope*math.Sin(2*math.Pi*burstHz*t) +
			steadyAmp*math.Sin(2*math.Pi*ultrasonicSteadyToneHz*t) + noise
		binary.LittleEndian.PutUint16(pcm[i*bytesPerPCM16Sample:], uint16(int16(math.Round(v)))) //nolint:gosec // G115: intentional int16 to uint16 bit reinterpretation for PCM
	}
	return pcm
}

// TestApplyUltrasonicFilter_SplitFollowsModelRate verifies that the filter splits
// the analysis window at 20 kHz of the model's own rate for every source rate.
// The window is at the model's rate (Bat 256 kHz, BSG-BAT 384 kHz) whatever the
// source rate, so a split computed from the source rate lands at the wrong
// frequency: a 25 kHz burst must count as ultrasonic content, and a 15 kHz burst
// above a steady 30 kHz tone must not.
func TestApplyUltrasonicFilter_SplitFollowsModelRate(t *testing.T) {
	models := []struct {
		id   string
		rate int
	}{
		{classifier.RegistryIDBat, 256000},
		{classifier.RegistryIDBSGBat, 384000},
	}
	sourceRates := []int{192000, 256000, 384000}

	for _, m := range models {
		spec, ok := classifier.GetModelSpec(m.id)
		require.True(t, ok)
		require.Equal(t, m.rate, spec.EffectiveSampleRate(), "test premise: model window rate")

		for _, sourceRate := range sourceRates {
			t.Run(fmt.Sprintf("%s_source_%dHz", m.id, sourceRate), func(t *testing.T) {
				p, settings := newUltrasonicFilterFixtureAt(t, sourceRate)

				// Bursts above the split: the band is bursty, so not unlikely.
				above := synthUltrasonicWindowS16LE(m.rate, ultrasonicBurstToneHz, ultrasonicTestAmplitude, 0)
				assert.False(t, runUltrasonicFilterForModel(p, settings, m.id, above),
					"a 25 kHz burst is ultrasonic content and must not be tagged unlikely")

				// Bursts below the split over a steady tone above it: the band is flat.
				below := synthUltrasonicWindowS16LE(m.rate, ultrasonicLowBurstHz, ultrasonicTestAmplitude, ultrasonicSteadyAmp)
				assert.True(t, runUltrasonicFilterForModel(p, settings, m.id, below),
					"a burst below 20 kHz must not count; the steady ultrasonic band is unlikely")
			})
		}
	}
}

// TestApplyUltrasonicFilter_UnknownModelIsNotFiltered verifies that a chunk whose
// model is unknown or unset is skipped: its window rate cannot be known.
func TestApplyUltrasonicFilter_UnknownModelIsNotFiltered(t *testing.T) {
	p, settings := newUltrasonicFilterFixture(t)
	pcm := flatUltrasonicToneS16LE()
	require.True(t, runUltrasonicFilterForModel(p, settings, classifier.RegistryIDBat, pcm))

	assert.False(t, runUltrasonicFilterForModel(p, settings, "", pcm), "an empty model ID must not filter")
	assert.False(t, runUltrasonicFilterForModel(p, settings, "NoSuchModel", pcm), "an unknown model must not filter")
}
