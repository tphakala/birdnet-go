package audio

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestSpectrumRequested(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		param    string
		sourceID string
		want     bool
	}{
		{"absent means opted out", "", "src-1", false},
		{"1 selects every source", "1", "src-1", true},
		{"true selects every source", "true", "src-2", true},
		{"source id selects that source", "src-1", "src-1", true},
		{"source id excludes other sources", "src-1", "src-2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, spectrumRequested(tt.param, tt.sourceID))
		})
	}
}

func TestFilterSpectrum(t *testing.T) {
	t.Parallel()

	sample := func() audiocore.AudioLevelData {
		return audiocore.AudioLevelData{
			Level:              42,
			Source:             "src-1",
			Spectrum:           []byte{1, 2, 3},
			SpectrumSampleRate: 48000,
			SpectrumTime:       1750000000,
		}
	}

	t.Run("strips every spectrum field when the client did not opt in", func(t *testing.T) {
		t.Parallel()
		data := sample()
		filterSpectrum(&data, "")
		assert.Nil(t, data.Spectrum)
		assert.Zero(t, data.SpectrumSampleRate)
		assert.Zero(t, data.SpectrumTime)
		assert.Equal(t, 42, data.Level, "the level itself must survive")
	})

	t.Run("keeps the spectrum for the requested source", func(t *testing.T) {
		t.Parallel()
		data := sample()
		filterSpectrum(&data, "src-1")
		assert.Equal(t, []byte{1, 2, 3}, data.Spectrum)
		assert.Equal(t, 48000, data.SpectrumSampleRate)
	})

	t.Run("strips the spectrum for a source the client did not select", func(t *testing.T) {
		t.Parallel()
		data := sample()
		filterSpectrum(&data, "src-2")
		assert.Nil(t, data.Spectrum)
	})
}

func TestWithoutSpectrumDoesNotMutateItsArgument(t *testing.T) {
	t.Parallel()

	original := audiocore.AudioLevelData{
		Source:             "src-1",
		Spectrum:           []byte{1, 2, 3},
		SpectrumSampleRate: 48000,
		SpectrumTime:       1750000000,
	}

	stripped := withoutSpectrum(original)

	assert.Nil(t, stripped.Spectrum)
	assert.Equal(t, []byte{1, 2, 3}, original.Spectrum, "the caller's copy must be untouched")
	assert.Equal(t, 48000, original.SpectrumSampleRate)
}

// TestAudioLevelSSEDataSpectrumWireFormat pins the JSON the browser fallback
// parses: the spectrum fields appear only when the handler kept them, so a
// client that did not opt in sees exactly the payload it saw before.
func TestAudioLevelSSEDataSpectrumWireFormat(t *testing.T) {
	t.Parallel()

	t.Run("omitted when the client did not opt in", func(t *testing.T) {
		t.Parallel()
		payload, err := json.Marshal(AudioLevelSSEData{
			Type:   "audio-level",
			Levels: map[string]audiocore.AudioLevelData{"src-1": {Level: 42, Source: "src-1"}},
		})
		require.NoError(t, err)
		assert.NotContains(t, string(payload), "spectrum")
	})

	t.Run("carried as base64 with its sample rate and capture time", func(t *testing.T) {
		t.Parallel()
		payload, err := json.Marshal(AudioLevelSSEData{
			Type: "audio-level",
			Levels: map[string]audiocore.AudioLevelData{"src-1": {
				Level:              42,
				Source:             "src-1",
				Spectrum:           []byte{0, 128, 255},
				SpectrumSampleRate: 48000,
				SpectrumTime:       1750000000.5,
			}},
		})
		require.NoError(t, err)

		var decoded struct {
			Levels map[string]struct {
				Spectrum           []byte  `json:"spectrum"`
				SpectrumSampleRate int     `json:"spectrumSampleRate"`
				SpectrumTime       float64 `json:"spectrumTime"`
			} `json:"levels"`
		}
		require.NoError(t, json.Unmarshal(payload, &decoded))

		entry := decoded.Levels["src-1"]
		assert.Equal(t, []byte{0, 128, 255}, entry.Spectrum)
		assert.Equal(t, 48000, entry.SpectrumSampleRate)
		assert.InDelta(t, 1750000000.5, entry.SpectrumTime, 0.001)
	})
}
