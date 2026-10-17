package conf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	dspeq "github.com/tphakala/go-audio-dsp/equalizer"
)

// eqFallbackConfigPath is the frontend copy of EqFilterConfig, used by the
// equalizer editor when the config endpoint cannot be reached.
const eqFallbackConfigPath = "../../frontend/src/lib/desktop/features/settings/components/eqFilterFallbackConfig.json"

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

// TestEqFilterConfigPassesDefaultsOff checks that every filter type offers 0
// passes (off), presets a new filter to 0 passes, and documents 0 in its tooltip.
func TestEqFilterConfigPassesDefaultsOff(t *testing.T) {
	t.Parallel()

	for name, cfg := range EqFilterConfig {
		found := false
		for _, p := range cfg.Parameters {
			if p.Name != "Passes" {
				continue
			}
			found = true
			assert.InDelta(t, 0, p.Min, 0, "%s: Passes Min", name)
			assert.InDelta(t, 0, p.Default, 0, "%s: Passes Default", name)
			assert.InDelta(t, 4, p.Max, 0, "%s: Passes Max", name)
			assert.Contains(t, p.Tooltip, "0=off", "%s: Passes tooltip", name)
		}
		assert.True(t, found, "%s has no Passes parameter", name)
	}
}

// TestEqFilterConfigHasNoTypographicDashes checks that no label or tooltip in
// EqFilterConfig contains an em dash or an en dash.
func TestEqFilterConfigHasNoTypographicDashes(t *testing.T) {
	t.Parallel()

	const dashes = "\u2014\u2013"
	for name, cfg := range EqFilterConfig {
		assert.False(t, strings.ContainsAny(cfg.Tooltip, dashes), "%s: type tooltip", name)
		for _, p := range cfg.Parameters {
			assert.False(t, strings.ContainsAny(p.Label, dashes), "%s.%s: label", name, p.Name)
			assert.False(t, strings.ContainsAny(p.Tooltip, dashes), "%s.%s: tooltip", name, p.Name)
		}
	}
}

// TestEqFilterConfigMatchesFrontendFallback checks that the fallback table the
// equalizer editor uses when the config endpoint fails is identical to
// EqFilterConfig, so the two cannot drift apart.
func TestEqFilterConfigMatchesFrontendFallback(t *testing.T) {
	t.Parallel()

	got, err := os.ReadFile(filepath.FromSlash(eqFallbackConfigPath))
	require.NoError(t, err)

	want, err := json.MarshalIndent(EqFilterConfig, "", "  ")
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got), "update %s to match conf.EqFilterConfig", eqFallbackConfigPath)
}

// TestEqFilterConfigMaxMatchesValidation checks that the editor table accepts
// every frequency and Q the config validation accepts, so the editor never
// pulls a valid stored value down to a smaller maximum.
func TestEqFilterConfigMaxMatchesValidation(t *testing.T) {
	t.Parallel()

	for name, cfg := range EqFilterConfig {
		for _, p := range cfg.Parameters {
			switch p.Name {
			case "Frequency":
				assert.InDelta(t, MaxEQFrequency, p.Max, 0, "%s.Frequency max", name)
			case "Q":
				assert.InDelta(t, MaxEQQ, p.Max, 0, "%s.Q max", name)
			}
		}
	}
}
