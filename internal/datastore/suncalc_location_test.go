package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestNew_SunCalcFollowsLocationChange pins that the legacy datastore's sun calculator follows a
// station location changed through the settings (a new published snapshot), and that the
// per-row sun-times lookup does not keep serving the previous location's times for a date it
// already resolved. It publishes global settings, so it must not run in parallel.
func TestNew_SunCalcFollowsLocationChange(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.BirdNET.Latitude = 60.1699 // Helsinki
	settings.BirdNET.Longitude = 24.9384
	conf.StoreSettings(settings)

	store, ok := New(settings).(*SQLiteStore)
	require.True(t, ok, "SQLite settings must yield a SQLiteStore")
	require.NotNil(t, store.SunCalc)

	const dateStr = "2024-06-21"
	// getSunEventsForDate resolves a server-local date through sunEventAnchor, so the fixed
	// calculator below is asked for the same instant.
	anchor := sunEventAnchor(time.Date(2024, 6, 21, 0, 0, 0, 0, time.Local))
	helsinki, err := store.getSunEventsForDate(dateStr)
	require.NoError(t, err)
	assert.Equal(t, "Europe/Helsinki", store.SunCalc.LocationName())

	moved := conf.CloneSettings(settings)
	moved.BirdNET.Latitude = -33.8688 // Sydney
	moved.BirdNET.Longitude = 151.2093
	conf.StoreSettings(moved)

	// Sun times first: the per-row lookup alone must notice the change (LocationName would swap
	// the calculator first and hide a lookup that skips it).
	sydney, err := store.getSunEventsForDate(dateStr)
	require.NoError(t, err)
	assert.False(t, helsinki.Sunrise.Equal(sydney.Sunrise),
		"sun times for an already-resolved date must be recalculated for the new location")
	want, err := suncalc.NewSunCalc(moved.BirdNET.Latitude, moved.BirdNET.Longitude).GetSunEventTimes(anchor)
	require.NoError(t, err)
	assert.True(t, want.Sunrise.Equal(sydney.Sunrise), "sunrise must match a fixed Sydney calculator")
	assert.True(t, want.Sunset.Equal(sydney.Sunset), "sunset must match a fixed Sydney calculator")
	assert.Equal(t, "Australia/Sydney", store.SunCalc.LocationName(),
		"the datastore's sun calculator must follow the published station location")
}
