// weather_precipitation_test.go: precipitation fields in the weather API response.

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestBuildHourlyWeatherResponse_PrecipitationFields verifies the hourly
// weather API response exposes the precipitation amount and type, and omits
// them (omitempty) when there is no precipitation.
func TestBuildHourlyWeatherResponse_PrecipitationFields(t *testing.T) {
	_, _, controller := setupWeatherTestEnvironment(t)

	wet := controller.buildHourlyWeatherResponse(&datastore.HourlyWeather{
		Time:              time.Date(2026, 1, 13, 12, 0, 0, 0, time.Local),
		Precipitation:     1.8,
		PrecipitationType: "snow",
		WeatherMain:       "Snow",
	})
	assert.InDelta(t, 1.8, wet.Precipitation, 0.001)
	assert.Equal(t, "snow", wet.PrecipitationType)

	data, err := json.Marshal(wet)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"precipitation":1.8`)
	assert.Contains(t, string(data), `"precipitation_type":"snow"`)

	// No precipitation: both fields must be omitted from the JSON.
	dry := controller.buildHourlyWeatherResponse(&datastore.HourlyWeather{
		Time:        time.Date(2026, 1, 13, 13, 0, 0, 0, time.Local),
		WeatherMain: "Clear",
	})
	dryData, err := json.Marshal(dry)
	require.NoError(t, err)
	dryStr := string(dryData)
	// Both precipitation keys must be omitted when there is no precipitation.
	assert.NotContains(t, dryStr, `"precipitation"`)
	assert.NotContains(t, dryStr, `"precipitation_type"`)
	// ...while other set fields are still emitted (omitempty must not over-drop).
	assert.Contains(t, dryStr, `"weather_main":"Clear"`)
}
