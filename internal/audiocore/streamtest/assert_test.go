package streamtest

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// synthToneS16LE builds mono s16le PCM of a sine wave, for exercising the
// signal-analysis helpers without any audio pipeline.
func synthToneS16LE(freq float64, sampleRate, samples int, amplitude float64) []byte {
	buf := make([]byte, samples*2)
	for i := range samples {
		v := amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
		s := int16(math.Round(v * math.MaxInt16))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	return buf
}

func TestDominantFrequency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		freq       float64
		sampleRate int
	}{
		{name: "1kHz at 48k", freq: 1000, sampleRate: 48000},
		{name: "440Hz at 48k", freq: 440, sampleRate: 48000},
		{name: "2kHz at 96k", freq: 2000, sampleRate: 96000},
		{name: "1kHz at 16k", freq: 1000, sampleRate: 16000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// 0.5 s of tone is plenty of resolution for a coarse+fine scan.
			pcm := synthToneS16LE(tt.freq, tt.sampleRate, tt.sampleRate/2, 0.5)
			got := DominantFrequency(pcm, tt.sampleRate)
			// Within 1% of the true tone.
			assert.InEpsilon(t, tt.freq, got, 0.01)
		})
	}
}

func TestRMSDBFS(t *testing.T) {
	t.Parallel()

	t.Run("full-scale sine is about -3 dBFS", func(t *testing.T) {
		t.Parallel()
		pcm := synthToneS16LE(1000, 48000, 48000, 1.0)
		// A full-scale sine has RMS = 1/sqrt(2) => 20*log10(0.707) = -3.01 dBFS.
		assert.InDelta(t, -3.01, RMSDBFS(pcm), 0.2)
	})

	t.Run("half-scale sine is about -9 dBFS", func(t *testing.T) {
		t.Parallel()
		pcm := synthToneS16LE(1000, 48000, 48000, 0.5)
		assert.InDelta(t, -9.03, RMSDBFS(pcm), 0.2)
	})

	t.Run("silence is deeply negative", func(t *testing.T) {
		t.Parallel()
		pcm := make([]byte, 48000*2)
		assert.Less(t, RMSDBFS(pcm), -100.0)
	})

	t.Run("empty input is deeply negative", func(t *testing.T) {
		t.Parallel()
		assert.Less(t, RMSDBFS(nil), -100.0)
	})
}

func TestDominantFrequencyRejectsShortInput(t *testing.T) {
	t.Parallel()
	// Fewer than one full sample pair should not panic; it returns 0.
	got := DominantFrequency([]byte{0x01}, 48000)
	require.Zero(t, got)
}
