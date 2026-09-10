package vad

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		probs     []float32
		minConsec int
		want      float32
	}{
		{name: "empty returns zero", probs: nil, minConsec: 3, want: 0},
		{name: "single high spike below sustain is rejected", probs: []float32{0.1, 0.95, 0.1, 0.1}, minConsec: 3, want: 0.1},
		{name: "three-frame run is accepted at the run minimum", probs: []float32{0.1, 0.8, 0.9, 0.85, 0.1}, minConsec: 3, want: 0.8},
		{name: "sustained speech reports high", probs: []float32{0.9, 0.92, 0.95, 0.93}, minConsec: 3, want: 0.92},
		{name: "minConsec 1 degrades to plain max", probs: []float32{0.2, 0.7, 0.3}, minConsec: 1, want: 0.7},
		{name: "fewer frames than sustain returns zero", probs: []float32{0.9, 0.9}, minConsec: 3, want: 0},
		{name: "zero minConsec treated as one", probs: []float32{0.4, 0.6}, minConsec: 0, want: 0.6},
		{name: "all low stays low", probs: []float32{0.01, 0.02, 0.03, 0.01}, minConsec: 3, want: 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := aggregate(tt.probs, tt.minConsec)
			assert.InDelta(t, tt.want, got, 1e-6)
		})
	}
}

// makeSinePCM16 builds sampleCount samples of a sine wave as 16-bit LE PCM bytes.
func makeSinePCM16(t *testing.T, sampleCount, sampleRate int, freqHz float64) []byte {
	t.Helper()
	buf := make([]byte, sampleCount*bytesPerSample)
	for i := range sampleCount {
		v := math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate)) * 0.5 * pcm16Scale
		s := int16(v)                                                    //nolint:gosec // G115: bounded test signal
		binary.LittleEndian.PutUint16(buf[i*bytesPerSample:], uint16(s)) //nolint:gosec // G115: PCM16 encode
	}
	return buf
}

func TestTo16k_PassthroughAt16k(t *testing.T) {
	t.Parallel()
	const n = 1600
	pcm := makeSinePCM16(t, n, sampleRate16k, 440)

	out, err := to16k(pcm, sampleRate16k)
	require.NoError(t, err)
	require.Len(t, out, n, "16k input must pass through with no length change")

	for i, f := range out {
		require.GreaterOrEqual(t, f, float32(-1.0), "sample %d below -1", i)
		require.LessOrEqual(t, f, float32(1.0), "sample %d above 1", i)
	}
}

func TestTo16k_Downsamples48k(t *testing.T) {
	t.Parallel()
	const n = 4800 // 100 ms at 48k
	pcm := makeSinePCM16(t, n, 48000, 440)

	out, err := to16k(pcm, 48000)
	require.NoError(t, err)

	ideal := n * sampleRate16k / 48000
	assert.NotEmpty(t, out)
	assert.LessOrEqual(t, len(out), ideal, "must not exceed the ideal ratio length")
	assert.Greater(t, len(out), ideal*3/4, "should be within warmup latency of ideal")
}

func TestTo16k_Empty(t *testing.T) {
	t.Parallel()
	out, err := to16k(nil, 48000)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestTo16k_OddBytes(t *testing.T) {
	t.Parallel()
	_, err := to16k([]byte{0x01, 0x02, 0x03}, 48000)
	require.Error(t, err)
}

func TestNew_RequiresModelPath(t *testing.T) {
	t.Parallel()
	_, err := New(Config{})
	require.ErrorIs(t, err, ErrModelPathRequired)
}

func TestSessionRun_Closed(t *testing.T) {
	t.Parallel()
	s := &session{}
	_, _, _, err := s.Run(make([]float32, modelInputSamples), make([]float32, stateWidth), make([]float32, stateWidth))
	require.ErrorIs(t, err, ErrSessionClosed)
}

func TestDetectorStrategyName(t *testing.T) {
	t.Parallel()
	d := &detector{}
	assert.Equal(t, StrategySequence, d.Strategy())
}
