package equalizer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildFilterChain_Disabled(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: false,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	assert.Nil(t, chain, "disabled EQ should return nil chain")
}

func TestBuildFilterChain_EmptyFilters(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	assert.Nil(t, chain, "empty filter list should return nil chain")
}

func TestBuildFilterChain_HighPass(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_LowPass(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowPass", Frequency: 15000, Q: 0.707, Passes: 2},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_BandReject(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "BandReject", Frequency: 1000, Width: 100, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_MultipleFilters(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 1},
			{Type: "LowPass", Frequency: 15000, Q: 0.707, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 2, chain.Length())
}

func TestBuildFilterChain_UnknownTypeSkipped(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 1},
			{Type: "UnknownFilter", Frequency: 500, Q: 1.0, Passes: 1},
			{Type: "LowPass", Frequency: 15000, Q: 0.707, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 2, chain.Length(), "unknown filter type should be skipped")
}

func TestBuildFilterChain_ZeroPassesDefaultsToOne(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 100, Q: 0.707, Passes: 0},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_BandPass(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "BandPass", Frequency: 2000, Width: 500, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_LowShelf(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "LowShelf", Frequency: 200, Q: 0.707, Gain: 6.0, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_HighShelf(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighShelf", Frequency: 8000, Q: 0.707, Gain: -3.0, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}

func TestBuildFilterChain_Peaking(t *testing.T) {
	t.Parallel()
	settings := conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "Peaking", Frequency: 4000, Width: 1000, Gain: 5.0, Passes: 1},
		},
	}
	chain := equalizer.BuildFilterChain(settings, 48000)
	require.NotNil(t, chain)
	assert.Equal(t, 1, chain.Length())
}
