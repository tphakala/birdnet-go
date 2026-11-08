// weather_sun_times_test.go: tests for the sun-times endpoint with a live-location SunCalc.

package weather

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Station locations the sun-times tests switch between.
const (
	helsinkiLatitude  = 60.1699
	helsinkiLongitude = 24.9384
	sydneyLatitude    = -33.8688
	sydneyLongitude   = 151.2093

	sunTimesDate = "2024-06-21"
)

// readsToComputeSunTimes counts the coordinate source reads SunCalc makes while it is
// constructed and computes the sun times of the test date, so the scripted source below
// switches location after exactly those reads and not after a hard-coded number.
func readsToComputeSunTimes(t *testing.T) int {
	t.Helper()
	reads := 0
	sc := suncalc.NewSunCalcWithSource(func() (float64, float64) {
		reads++
		return helsinkiLatitude, helsinkiLongitude
	})
	date, err := time.Parse(time.DateOnly, sunTimesDate)
	require.NoError(t, err)
	_, err = sc.GetSunEventTimes(date)
	require.NoError(t, err)
	return reads
}

// TestGetSunTimes_TimezoneMatchesReturnedTimes pins that the response's timezone belongs to the
// same location as its sun times when the station location changes during the request. The
// scripted source reports Helsinki until the handler has computed the sun times and Sydney on
// every later read, so a handler that asked the calculator for the timezone separately would
// label Helsinki times as Australia/Sydney.
func TestGetSunTimes_TimezoneMatchesReturnedTimes(t *testing.T) {
	// Source reads made before the location changes: construction, then the sun-times call.
	readsBeforeChange := readsToComputeSunTimes(t)
	reads := 0
	sc := suncalc.NewSunCalcWithSource(func() (float64, float64) {
		reads++
		if reads <= readsBeforeChange {
			return helsinkiLatitude, helsinkiLongitude
		}
		return sydneyLatitude, sydneyLongitude
	})

	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(mocks.NewMockInterface(t)),
		apitest.WithSunCalc(sc))
	handler := New(core)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/weather/sun/"+sunTimesDate, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("date")
	ctx.SetParamValues(sunTimesDate)

	require.NoError(t, handler.GetSunTimes(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var got sunTimesResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "Europe/Helsinki", got.Timezone,
		"the timezone must come from the same state as the returned sun times")
}
