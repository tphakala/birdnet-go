package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetCivilDawn_DefinedAtMidLatitude verifies that at a mid-latitude location civil dawn is
// reported as defined and is strictly before sunrise (matching the cached GetSunEventTimes value).
func TestGetCivilDawn_DefinedAtMidLatitude(t *testing.T) {
	t.Parallel()

	// London, around the spring equinox: civil dawn is well-defined year-round here.
	sc := NewSunCalc(51.5074, -0.1278)
	date := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)

	civilDawn, ok := sc.GetCivilDawn(date)
	require.True(t, ok, "civil dawn should be defined at mid-latitude")

	events, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, civilDawn.Before(events.Sunrise), "civil dawn must precede sunrise")
	assert.Equal(t, events.CivilDawn, civilDawn, "GetCivilDawn returns the same instant as GetSunEventTimes")
}

// TestGetCivilDawn_UndefinedDuringPolarDay verifies that during polar day / white nights (when civil
// twilight does not occur and GetSunEventTimes either errors or substitutes sunrise for civil dawn),
// GetCivilDawn reports civil dawn as undefined so the dawn-onset analytics can treat it as a gap.
func TestGetCivilDawn_UndefinedDuringPolarDay(t *testing.T) {
	t.Parallel()

	// Svalbard at the summer solstice: the sun does not dip to civil twilight (or set at all).
	sc := NewSunCalc(78.2232, 15.6267)
	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	_, ok := sc.GetCivilDawn(date)
	assert.False(t, ok, "civil dawn should be undefined during polar day")
}
