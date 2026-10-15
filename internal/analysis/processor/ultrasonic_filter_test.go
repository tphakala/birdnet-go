package processor

import (
	"encoding/binary"
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
	registry := audiocore.NewSourceRegistry(audiocore.GetLogger())
	_, err := registry.Register(&audiocore.SourceConfig{
		ID:               ultrasonicTestSourceID,
		ConnectionString: ultrasonicTestConnection,
		DisplayName:      "Ultrasonic test",
		Type:             audiocore.SourceTypeAudioCard,
		SampleRate:       ultrasonicTestRate,
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
	item := classifier.Results{
		PCMdata: pcm,
		Source:  datastore.AudioSource{ID: ultrasonicTestConnection},
	}
	detections := []Detections{{Result: detection.Result{}}}
	p.applyUltrasonicFilter(settings, item, detections)
	return detections[0].Result.Unlikely
}
