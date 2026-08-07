package conf

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rare-species highlight threshold is an occurrence probability and must stay
// within [0, 1]; validateDashboardSettings clamps out-of-range or non-finite values.
func TestValidateDashboardSettings_RarityThresholdClamped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"in-range unchanged", 0.25, 0.25},
		{"lower boundary kept", 0, 0},
		{"upper boundary kept", 1, 1},
		{"above range clamped to 1", 1.5, 1},
		{"below range clamped to 0", -0.2, 0},
		{"NaN reset to default", math.NaN(), DefaultRarityHighlightThreshold},
		{"positive infinity reset to default", math.Inf(1), DefaultRarityHighlightThreshold},
		{"negative infinity reset to default", math.Inf(-1), DefaultRarityHighlightThreshold},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Dashboard{Rarity: RarityHighlight{Enabled: true, Threshold: tt.in}}

			require.NoError(t, validateDashboardSettings(settings))
			assert.InDelta(t, tt.want, settings.Rarity.Threshold, 1e-9)
		})
	}
}

// The default highlight threshold is 25% occurrence, matching the UI default.
func TestDefaultRarityHighlightThreshold(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 0.25, DefaultRarityHighlightThreshold, 1e-9)
}
