package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"
)

// TestEQFilterType checks every stored filter type name resolves to the matching
// library type, and an unknown name does not resolve.
func TestEQFilterType(t *testing.T) {
	t.Parallel()

	want := map[string]dspeq.FilterType{
		"LowPass":    dspeq.LowPass,
		"HighPass":   dspeq.HighPass,
		"AllPass":    dspeq.AllPass,
		"BandPass":   dspeq.BandPass,
		"BandReject": dspeq.BandReject,
		"LowShelf":   dspeq.LowShelf,
		"HighShelf":  dspeq.HighShelf,
		"Peaking":    dspeq.Peaking,
	}
	for name, ft := range want {
		got, ok := EQFilterType(name)
		assert.True(t, ok, name)
		assert.Equal(t, ft, got, name)
	}

	for _, name := range []string{"", "lowpass", "Notch"} {
		_, ok := EQFilterType(name)
		assert.False(t, ok, "%q must not resolve", name)
	}
}

// TestEQFilterUsesQ checks the Q-based types are reported as using Q, and that
// the width-based and unknown types are not.
func TestEQFilterUsesQ(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"LowPass", "HighPass", "AllPass", "LowShelf", "HighShelf"} {
		assert.True(t, eqFilterUsesQ(name), name)
	}
	for _, name := range []string{"BandPass", "BandReject", "Peaking", "", "Notch"} {
		assert.False(t, eqFilterUsesQ(name), name)
	}
}

// TestEqFilterConfigMatchesLibrary checks that each type in the EqFilterConfig
// parameter table lists the Q, Width and Gain parameters exactly when the
// equalizer library type uses them.
func TestEqFilterConfigMatchesLibrary(t *testing.T) {
	t.Parallel()

	for name, cfg := range EqFilterConfig {
		ft, ok := EQFilterType(name)
		if !assert.True(t, ok, "EqFilterConfig type %q has no library type", name) {
			continue
		}
		params := make(map[string]bool, len(cfg.Parameters))
		for _, p := range cfg.Parameters {
			params[p.Name] = true
		}
		assert.Equal(t, ft.UsesQ(), params["Q"], "%s: Q parameter", name)
		assert.Equal(t, ft.UsesWidth(), params["Width"], "%s: Width parameter", name)
		assert.Equal(t, ft.UsesGain(), params["Gain"], "%s: Gain parameter", name)
	}
}
