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

	assert.Equal(t, "New Moon", result.PhaseName)
	assert.Equal(t, "moon-new", result.IconName)
	assert.InDelta(t, 0, result.Illumination, 15) // Low illumination for new moon
}

func TestGetMoonPhase_FullMoon(t *testing.T) {
	// Jan 13, 2025 is approximately a full moon
	date := time.Date(2025, 1, 13, 12, 0, 0, 0, time.UTC)
	result := GetMoonPhase(date)

	assert.Equal(t, "Full Moon", result.PhaseName)
	assert.Equal(t, "moon-full", result.IconName)
	assert.InDelta(t, 100, result.Illumination, 15) // High illumination for full moon
}

func TestGetMoonPhase_AllPhasesHaveValidIconNames(t *testing.T) {
	validIcons := map[string]bool{
		"moon-new":             true,
		"moon-waxing-crescent": true,
		"moon-first-quarter":   true,
		"moon-waxing-gibbous":  true,
		"moon-full":            true,
		"moon-waning-gibbous":  true,
		"moon-last-quarter":    true,
		"moon-waning-crescent": true,
	}

	// Test across 28 consecutive days to cover all phases
	startDate := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range 28 {
		date := startDate.AddDate(0, 0, i)
		result := GetMoonPhase(date)

		assert.True(t, validIcons[result.IconName],
			"day %d: invalid icon name %q for phase %.2f", i, result.IconName, result.Phase)
		assert.GreaterOrEqual(t, result.Illumination, 0.0)
		assert.LessOrEqual(t, result.Illumination, 100.0)
		assert.NotEmpty(t, result.PhaseName)
	}
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
		{"New Moon", "🌑"},
		{"Waxing Crescent", "🌒"},
		{"First Quarter", "🌓"},
		{"Waxing Gibbous", "🌔"},
		{"Full Moon", "🌕"},
		{"Waning Gibbous", "🌖"},
		{"Last Quarter", "🌗"},
		{"Waning Crescent", "🌘"},
		{"Unknown", "🌑"}, // fallback
	}

	for _, tt := range tests {
		t.Run(tt.phaseName, func(t *testing.T) {
			assert.Equal(t, tt.expected, MoonPhaseEmoji(tt.phaseName))
		})
	}
}
