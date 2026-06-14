package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestV2OnlyDatastore_HourlyWeather_PrecipitationRoundTrip verifies the
// precipitation amount/type and weather_main fields survive a full
// save -> read round-trip through the v2only datastore (DTO -> v2 entity ->
// DTO). It also exercises that AutoMigrate created the precipitation columns,
// since a missing column would make SaveHourlyWeather fail.
func TestV2OnlyDatastore_HourlyWeather_PrecipitationRoundTrip(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	const date = "2026-01-13"
	require.NoError(t, ds.SaveDailyEvents(&datastore.DailyEvents{
		Date:     date,
		Country:  "FI",
		CityName: "Helsinki",
	}))
	daily, err := ds.GetDailyEvents(date)
	require.NoError(t, err)

	obsTime := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
	want := &datastore.HourlyWeather{
		DailyEventsID:     daily.ID,
		Time:              obsTime,
		Temperature:       -2.5,
		Clouds:            90,
		Precipitation:     1.8,
		PrecipitationType: "snow",
		WeatherMain:       "Snow",
		WeatherDesc:       "light snow",
		WeatherIcon:       "13",
	}
	require.NoError(t, ds.SaveHourlyWeather(want))

	t.Run("GetHourlyWeather", func(t *testing.T) {
		got, err := ds.GetHourlyWeather(date)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.InDelta(t, 1.8, got[0].Precipitation, 0.001)
		assert.Equal(t, "snow", got[0].PrecipitationType)
		assert.Equal(t, "Snow", got[0].WeatherMain)
	})

	t.Run("LatestHourlyWeather", func(t *testing.T) {
		got, err := ds.LatestHourlyWeather()
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.InDelta(t, 1.8, got.Precipitation, 0.001)
		assert.Equal(t, "snow", got.PrecipitationType)
		assert.Equal(t, "Snow", got.WeatherMain)
	})
}

// TestV2OnlyDatastore_SaveHourlyWeather_NilInput verifies SaveHourlyWeather
// rejects a nil record instead of panicking, matching the legacy datastore's
// nil guard.
func TestV2OnlyDatastore_SaveHourlyWeather_NilInput(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	err := ds.SaveHourlyWeather(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")
}
