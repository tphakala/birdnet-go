package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveStreamSampleRates covers the probe-failure fallback logic that
// keeps a reconnecting RTSP source from silently collapsing to the target rate
// (#4350). targetRate is the 48 kHz analysis default in every case.
func TestResolveStreamSampleRates(t *testing.T) {
	t.Parallel()

	const target = 48000

	tests := []struct {
		name       string
		probeRate  int
		fallback   int
		isBat      bool
		wantSource int
		wantOutput int
		wantRetain bool
		wantEscal  bool
	}{
		{
			name:       "probe ok, non-bat",
			probeRate:  44100,
			isBat:      false,
			wantSource: 44100,
			wantOutput: target,
		},
		{
			name:       "probe ok, bat above target raises output",
			probeRate:  250000,
			isBat:      true,
			wantSource: 250000,
			wantOutput: 250000,
		},
		{
			name:       "probe ok, bat at or below target keeps target output",
			probeRate:  32000,
			isBat:      true,
			wantSource: 32000,
			wantOutput: target,
		},
		{
			name:       "probe failed, fallback known, non-bat retains source rate",
			probeRate:  0,
			fallback:   96000,
			isBat:      false,
			wantSource: 96000,
			wantOutput: target,
			wantRetain: true,
		},
		{
			name:       "probe failed, fallback known, bat retains and raises output",
			probeRate:  0,
			fallback:   250000,
			isBat:      true,
			wantSource: 250000,
			wantOutput: 250000,
			wantRetain: true,
		},
		{
			name:       "probe failed, fallback below target, bat retains source but keeps target output",
			probeRate:  0,
			fallback:   32000,
			isBat:      true,
			wantSource: 32000,
			wantOutput: target,
			wantRetain: true,
		},
		{
			name:       "probe failed, no fallback, bat escalates",
			probeRate:  0,
			fallback:   0,
			isBat:      true,
			wantSource: 0,
			wantOutput: target,
			wantEscal:  true,
		},
		{
			name:       "probe failed, no fallback, non-bat stays quiet",
			probeRate:  0,
			fallback:   0,
			isBat:      false,
			wantSource: 0,
			wantOutput: target,
		},
		{
			name:       "successful probe never retains even with a fallback present",
			probeRate:  192000,
			fallback:   250000,
			isBat:      true,
			wantSource: 192000,
			wantOutput: 192000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotSource, gotOutput, gotRetain, gotEscal := resolveStreamSampleRates(
				tc.probeRate, tc.fallback, target, tc.isBat)
			assert.Equal(t, tc.wantSource, gotSource, "source rate")
			assert.Equal(t, tc.wantOutput, gotOutput, "output rate")
			assert.Equal(t, tc.wantRetain, gotRetain, "retained flag")
			assert.Equal(t, tc.wantEscal, gotEscal, "escalate flag")
		})
	}
}

// TestResolveStreamChannels covers independent channel recovery: a probe that
// fails to report a channel count must recover it from the fallback so a
// left/right selection is not silently downmixed (#4350).
func TestResolveStreamChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		probeChannels int
		fallback      int
		wantChannels  int
		wantRetained  bool
	}{
		{"probe ok keeps probed channels", 2, 6, 2, false},
		{"probe ok mono keeps mono", 1, 2, 1, false},
		{"probe zero recovers fallback", 0, 2, 2, true},
		{"probe zero no fallback stays zero", 0, 0, 0, false},
		{"probe zero fallback mono recovers mono", 0, 1, 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotChannels, gotRetained := resolveStreamChannels(tc.probeChannels, tc.fallback)
			assert.Equal(t, tc.wantChannels, gotChannels, "channels")
			assert.Equal(t, tc.wantRetained, gotRetained, "retained flag")
		})
	}
}

// TestStreamFallbackKnown verifies the guard that decides whether a fallback
// carries any usable value.
func TestStreamFallbackKnown(t *testing.T) {
	t.Parallel()

	assert.False(t, streamFallback{}.known(), "zero value is not known")
	assert.True(t, streamFallback{sampleRate: 48000}.known(), "sample rate makes it known")
	assert.True(t, streamFallback{channels: 2}.known(), "channel count makes it known")
	assert.True(t, streamFallback{sampleRate: 48000, channels: 2}.known(), "both set is known")
}
