package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestNewStationSunCalc_FollowsLocationChange pins that the sun calculator the API service shares
// with the processor, quiet hours, the nighttime scheduler and the API follows a station location
// published after it was built. It publishes global settings, so it must not run in parallel.
func TestNewStationSunCalc_FollowsLocationChange(t *testing.T) {
	const (
		helsinkiLatitude  = 60.1699
		helsinkiLongitude = 24.9384
		sydneyLatitude    = -33.8688
		sydneyLongitude   = 151.2093
	)
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	settings := &conf.Settings{}
	settings.BirdNET.Latitude = helsinkiLatitude
	settings.BirdNET.Longitude = helsinkiLongitude
	conf.StoreSettings(settings)

	sc := newStationSunCalc(settings)
	require.NotNil(t, sc)

	moved := conf.CloneSettings(settings)
	moved.BirdNET.Latitude = sydneyLatitude
	moved.BirdNET.Longitude = sydneyLongitude
	conf.StoreSettings(moved)

	date := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	got, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	want, err := suncalc.NewSunCalc(sydneyLatitude, sydneyLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, want.Sunrise.Equal(got.Sunrise), "sunrise must match a fixed Sydney calculator")
	assert.Equal(t, "Australia/Sydney", got.Sunrise.Location().String(),
		"sun times must be in the new location's timezone")
}
