package equalizer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildFilterChainReport_ReportsSkips(t *testing.T) {
	t.Parallel()
	good := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1}
	tests := []struct {
		name       string
		bad        conf.EqualizerFilter
		wantReason equalizer.SkipReason
		wantErr    bool
	}{
		{"unknown type", conf.EqualizerFilter{Type: "BandStop", Frequency: 1000, Q: 1, Passes: 1}, equalizer.SkipUnknownType, false},
		{"corner above Nyquist", conf.EqualizerFilter{Type: "HighPass", Frequency: 30000, Q: butterworthQ, Passes: 1}, equalizer.SkipInvalidParameters, true},
		{"zero Q on LowPass", conf.EqualizerFilter{Type: "LowPass", Frequency: 1000, Q: 0, Passes: 1}, equalizer.SkipInvalidParameters, true},
		{"too many passes", conf.EqualizerFilter{Type: "LowPass", Frequency: 1000, Q: butterworthQ, Passes: dspeq.MaxPasses + 1}, equalizer.SkipInvalidParameters, true},
		{"negative passes", conf.EqualizerFilter{Type: "LowPass", Frequency: 1000, Q: butterworthQ, Passes: -1}, equalizer.SkipInvalidParameters, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// The bad filter sits at index 1 so the index is not just 0.
			chain, skipped := equalizer.BuildFilterChainReport(enabled(good, tt.bad), testRate)
			require.NotNil(t, chain)
			assert.Equal(t, 1, chain.Len())
			require.Len(t, skipped, 1)
			assert.Equal(t, 1, skipped[0].Index)
			assert.Equal(t, tt.bad.Type, skipped[0].Type)
			assert.Equal(t, tt.wantReason, skipped[0].Reason)
			if tt.wantErr {
				require.Error(t, skipped[0].Err)
				assert.InDelta(t, tt.bad.Frequency, skipped[0].Band.Frequency, 0)
			} else {
				assert.NoError(t, skipped[0].Err)
			}
		})
	}
}

func TestBuildFilterChainReport_MatchesBuildFilterChain(t *testing.T) {
	t.Parallel()
	good := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 2}
	peak := conf.EqualizerFilter{Type: "Peaking", Frequency: 3000, Width: 1000, Gain: 4, Passes: 1}
	off := conf.EqualizerFilter{Type: "Peaking", Frequency: 3000, Width: 1000, Gain: 4, Passes: 0}
	unknown := conf.EqualizerFilter{Type: "NoSuchType", Frequency: 100, Q: 1, Passes: 1}
	tooHigh := conf.EqualizerFilter{Type: "LowPass", Frequency: 24000, Q: butterworthQ, Passes: 1}
	tests := []struct {
		name     string
		settings conf.EqualizerSettings
	}{
		{"disabled", conf.EqualizerSettings{Enabled: false, Filters: []conf.EqualizerFilter{good}}},
		{"empty", enabled()},
		{"all skipped", enabled(unknown, tooHigh)},
		{"mixed", enabled(good, unknown, peak, tooHigh)},
		{"mixed with bypassed", enabled(good, off, unknown, peak, tooHigh)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := equalizer.BuildFilterChain(tt.settings, testRate)
			got, _ := equalizer.BuildFilterChainReport(tt.settings, testRate)
			if want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, want.Len(), got.Len())
			assert.Equal(t, want.NumSections(), got.NumSections())
			in := testSignal(testSignalLen, testRate)
			assert.Equal(t, runChain(t, want, in), runChain(t, got, in))
		})
	}
}

func TestBuildFilterChainReport_NoSkipsIsEmpty(t *testing.T) {
	t.Parallel()
	good := conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1}
	_, skipped := equalizer.BuildFilterChainReport(enabled(good), testRate)
	assert.Empty(t, skipped)
	_, skipped = equalizer.BuildFilterChainReport(conf.EqualizerSettings{Filters: []conf.EqualizerFilter{good}}, testRate)
	assert.Empty(t, skipped, "a disabled equalizer reports no skips")
}

func TestBuildFilterChainReport_BypassedFilterIsNotReported(t *testing.T) {
	t.Parallel()
	settings := enabled(
		conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 0},
		conf.EqualizerFilter{Type: "HighPass", Frequency: 100, Q: butterworthQ, Passes: 1},
		conf.EqualizerFilter{Type: "LowPass", Frequency: 24000, Q: butterworthQ, Passes: 1},
		conf.EqualizerFilter{Type: "NoSuchType", Frequency: 100, Q: 1, Passes: 0},
	)
	chain, skipped := equalizer.BuildFilterChainReport(settings, testRate)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Len())
	require.Len(t, skipped, 2)
	// Indexes are positions in the settings, not among the applied filters.
	assert.Equal(t, 2, skipped[0].Index)
	assert.Equal(t, equalizer.SkipInvalidParameters, skipped[0].Reason)
	assert.Equal(t, 3, skipped[1].Index)
	assert.Equal(t, equalizer.SkipUnknownType, skipped[1].Reason, "an unknown type is reported even at passes 0")
}

// TestBuildFilterChainReport_UltrasonicNotchFollowsSampleRate pins that a notch
// above 24 kHz, which validation now accepts, is applied at an ultrasonic
// capture rate and skipped, not failed, at 48 kHz.
func TestBuildFilterChainReport_UltrasonicNotchFollowsSampleRate(t *testing.T) {
	t.Parallel()
	const batRate = 256000
	notch := conf.EqualizerFilter{Type: "BandReject", Frequency: 60000, Width: 1000, Passes: 1}

	t.Run("applied at the bat rate", func(t *testing.T) {
		t.Parallel()
		chain, skipped := equalizer.BuildFilterChainReport(enabled(notch), batRate)
		require.NotNil(t, chain)
		assert.Equal(t, 1, chain.Len())
		assert.Empty(t, skipped)
	})

	t.Run("skipped at 48 kHz", func(t *testing.T) {
		t.Parallel()
		chain, skipped := equalizer.BuildFilterChainReport(enabled(notch), testRate)
		assert.Nil(t, chain)
		require.Len(t, skipped, 1)
		assert.Equal(t, equalizer.SkipInvalidParameters, skipped[0].Reason)
		require.Error(t, skipped[0].Err)
	})
}
