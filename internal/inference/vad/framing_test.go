package vad

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		samples int
		want    int
	}{
		{name: "zero", samples: 0, want: 0},
		{name: "negative", samples: -10, want: 0},
		{name: "exact one frame", samples: windowSamples, want: 1},
		{name: "one sample rounds up to one frame", samples: 1, want: 1},
		{name: "partial tail rounds up", samples: windowSamples + 1, want: 2},
		{name: "3s at 16k is 94 frames", samples: 48000, want: 94},
		{name: "5s at 16k is 157 frames", samples: 80000, want: 157},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, frameCount(tt.samples))
		})
	}
}

// ramp returns n distinguishable samples: ramp(n)[i] = (i+1)/32768, so any
// misplaced copy in the framing layout shows up as a wrong value, not a
// coincidental zero match.
func ramp(n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(i+1) / pcm16Scale
	}
	return out
}

func TestStackFrames_Empty(t *testing.T) {
	t.Parallel()
	assert.Nil(t, stackFrames(nil, nil))
	assert.Nil(t, stackFrames([]float32{}, nil))
}

func TestStackFrames_SingleHopZeroContext(t *testing.T) {
	t.Parallel()
	samples := ramp(windowSamples)
	frames := stackFrames(samples, nil)
	require.Len(t, frames, modelInputSamples)
	for i := range contextSamples {
		assert.Zero(t, frames[i], "context sample %d must be zero", i)
	}
	assert.Equal(t, samples, frames[contextSamples:], "window must be the input samples")
}

func TestStackFrames_ContextThreadsBetweenHops(t *testing.T) {
	t.Parallel()
	const extra = 100 // partial third hop, zero padded
	samples := ramp(2*windowSamples + extra)
	frames := stackFrames(samples, nil)
	require.Len(t, frames, 3*modelInputSamples)

	row := func(f int) []float32 { return frames[f*modelInputSamples : (f+1)*modelInputSamples] }

	// Row 0: zero context, first window.
	for i := range contextSamples {
		require.Zero(t, row(0)[i])
	}
	assert.Equal(t, samples[:windowSamples], row(0)[contextSamples:])

	// Row 1: context = last 64 samples of window 0.
	assert.Equal(t, samples[windowSamples-contextSamples:windowSamples], row(1)[:contextSamples])
	assert.Equal(t, samples[windowSamples:2*windowSamples], row(1)[contextSamples:])

	// Row 2: context = last 64 samples of window 1; partial window zero padded.
	assert.Equal(t, samples[2*windowSamples-contextSamples:2*windowSamples], row(2)[:contextSamples])
	assert.Equal(t, samples[2*windowSamples:], row(2)[contextSamples:contextSamples+extra])
	for i := contextSamples + extra; i < modelInputSamples; i++ {
		assert.Zero(t, row(2)[i], "padding at %d must be zero", i)
	}
}

func TestStackFrames_PrevContextSeedsFirstRow(t *testing.T) {
	t.Parallel()
	samples := ramp(windowSamples)
	prev := make([]float32, contextSamples)
	for i := range prev {
		prev[i] = -float32(i+1) / pcm16Scale
	}
	frames := stackFrames(samples, prev)
	require.Len(t, frames, modelInputSamples)
	assert.Equal(t, prev, frames[:contextSamples], "row 0 context must be the carried prevContext")
	assert.Equal(t, samples, frames[contextSamples:])
}
