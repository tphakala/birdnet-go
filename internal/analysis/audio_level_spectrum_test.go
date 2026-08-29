package analysis

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sinePCM returns count 16-bit LE samples of a full-scale sine at freqHz.
func sinePCM(count, freqHz, sampleRate int) []byte {
	pcm := make([]byte, count*2)
	for i := range count {
		v := math.Sin(2 * math.Pi * float64(freqHz) * float64(i) / float64(sampleRate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(v*(pcm16Max-1)))) //nolint:gosec // G115: intentional int16→uint16 bit reinterpretation for PCM audio
	}
	return pcm
}

func TestSpectrumAnalyzerPeaksAtToneFrequency(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	const toneHz = 3000
	a := newSpectrumAnalyzer()

	bins := a.process(sinePCM(spectrumFFTSize, toneHz, sampleRate), time.Now())
	require.NotNil(t, bins, "a full window should produce a column")
	require.Len(t, bins, spectrumBinCount)

	peak := 0
	for i, v := range bins {
		if v > bins[peak] {
			peak = i
		}
	}

	expected := int(math.Round(float64(toneHz) * spectrumFFTSize / sampleRate))
	assert.InDelta(t, expected, peak, 1, "peak bin should match the tone frequency")
	assert.Greater(t, int(bins[peak]), 200, "a full-scale tone should be near the top of the dB range")
}

func TestSpectrumAnalyzerSilenceIsZero(t *testing.T) {
	t.Parallel()

	a := newSpectrumAnalyzer()
	bins := a.process(make([]byte, spectrumFFTSize*2), time.Now())
	require.NotNil(t, bins)

	for i, v := range bins {
		require.Zerof(t, v, "bin %d should be zero for digital silence", i)
	}
}

func TestSpectrumAnalyzerNeedsFullWindow(t *testing.T) {
	t.Parallel()

	const chunk = 256
	a := newSpectrumAnalyzer()
	now := time.Now()

	for range spectrumFFTSize/chunk - 1 {
		assert.Nil(t, a.process(sinePCM(chunk, 3000, 48000), now), "partial window must not emit")
	}
	assert.NotNil(t, a.process(sinePCM(chunk, 3000, 48000), now), "window completion should emit")
}

func TestSpectrumAnalyzerRateLimits(t *testing.T) {
	t.Parallel()

	a := newSpectrumAnalyzer()
	start := time.Now()
	pcm := sinePCM(spectrumFFTSize, 3000, 48000)

	require.NotNil(t, a.process(pcm, start))
	assert.Nil(t, a.process(pcm, start.Add(spectrumInterval/2)), "columns inside the interval are suppressed")
	assert.NotNil(t, a.process(pcm, start.Add(spectrumInterval)), "columns after the interval resume")
}

func TestSpectrumAnalyzerToleratesOddByteCount(t *testing.T) {
	t.Parallel()

	a := newSpectrumAnalyzer()
	pcm := sinePCM(spectrumFFTSize, 3000, 48000)

	assert.NotPanics(t, func() {
		a.process(append(pcm, 0x7f), time.Now())
	})
}

func TestSpectrumAnalyzerKeepsNewestSamplesOfLongFrame(t *testing.T) {
	t.Parallel()

	const sampleRate = 48000
	a := newSpectrumAnalyzer()

	// A frame four windows long: only its newest window should shape the column.
	// Prefix it with silence and end with a tone; the tone must still be found.
	pcm := append(make([]byte, spectrumFFTSize*3*2), sinePCM(spectrumFFTSize, 6000, sampleRate)...)
	bins := a.process(pcm, time.Now())
	require.NotNil(t, bins)

	peak := 0
	for i, v := range bins {
		if v > bins[peak] {
			peak = i
		}
	}
	expected := int(math.Round(6000 * float64(spectrumFFTSize) / sampleRate))
	assert.InDelta(t, expected, peak, 1)
}

// BenchmarkSpectrumAnalyzer measures the per-column cost, which every source
// pays continuously whether or not a client asked for spectrum data. Frames are
// sized like a typical router frame so the rolling-window copy is included.
func BenchmarkSpectrumAnalyzer(b *testing.B) {
	a := newSpectrumAnalyzer()
	pcm := sinePCM(2400, 3000, 48000) // 50ms at 48kHz
	now := time.Now()

	for b.Loop() {
		now = now.Add(spectrumInterval)
		a.process(pcm, now)
	}
}
