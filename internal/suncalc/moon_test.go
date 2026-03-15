// internal/suncalc/moon_test.go
package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetMoonPhase_NewMoon(t *testing.T) {
	// Jan 29, 2025 is approximately a new moon
	date := time.Date(2025, 1, 29, 12, 0, 0, 0, time.UTC)
	result := GetMoonPhase(date)

	assert.Equal(t, PhaseNewMoon, result.PhaseName)
	assert.Equal(t, IconNameNewMoon, result.IconName)
	assert.InDelta(t, 0, result.Illumination, 15) // Low illumination for new moon
}

func TestGetMoonPhase_FullMoon(t *testing.T) {
	// Jan 13, 2025 is approximately a full moon
	date := time.Date(2025, 1, 13, 12, 0, 0, 0, time.UTC)
	result := GetMoonPhase(date)

	assert.Equal(t, PhaseFullMoon, result.PhaseName)
	assert.Equal(t, IconNameFullMoon, result.IconName)
	assert.InDelta(t, 100, result.Illumination, 15) // High illumination for full moon
}

func TestGetMoonPhase_AllPhasesHaveValidIconNames(t *testing.T) {
	validIcons := make(map[string]bool, len(phases))
	for _, p := range phases {
		validIcons[p.iconName] = true
	}

	// Test across 28 consecutive days to cover all phases
	seenIcons := make(map[string]bool)
	startDate := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 28 {
		date := startDate.AddDate(0, 0, i)
		result := GetMoonPhase(date)

		assert.True(t, validIcons[result.IconName],
			"day %d: invalid icon name %q for phase %.2f", i, result.IconName, result.Phase)
		assert.GreaterOrEqual(t, result.Illumination, 0.0)
		assert.LessOrEqual(t, result.Illumination, 100.0)
		assert.NotEmpty(t, result.PhaseName)

		seenIcons[result.IconName] = true
	}

	// Assert that all 8 phase buckets were actually hit
	assert.Len(t, seenIcons, len(validIcons),
		"expected all %d moon phase icons to appear in 28 days, but only saw %d: %v",
		len(validIcons), len(seenIcons), seenIcons)
}

func TestGetMoonPhase_PhaseRange(t *testing.T) {
	date := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	result := GetMoonPhase(date)

	assert.GreaterOrEqual(t, result.Phase, 0.0)
	assert.Less(t, result.Phase, 28.0)
}

func TestMoonPhaseEmoji(t *testing.T) {
	tests := []struct {
		phaseName string
		expected  string
	}{
		{PhaseNewMoon, "🌑"},
		{PhaseWaxingCrescent, "🌒"},
		{PhaseFirstQuarter, "🌓"},
		{PhaseWaxingGibbous, "🌔"},
		{PhaseFullMoon, "🌕"},
		{PhaseWaningGibbous, "🌖"},
		{PhaseLastQuarter, "🌗"},
		{PhaseWaningCrescent, "🌘"},
		{"Unknown", "🌑"}, // fallback
	}

	for _, tt := range tests {
		t.Run(tt.phaseName, func(t *testing.T) {
			assert.Equal(t, tt.expected, MoonPhaseEmoji(tt.phaseName))
		})
	}
}
