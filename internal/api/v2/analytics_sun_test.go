package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// sunJSON mirrors the analytics sun-times wire shape. Event fields are pointers so an undefined
// event (polar day/night, or SunCalc unavailable) round-trips as JSON null rather than 0.
type sunJSON struct {
	Date      string `json:"date"`
	Sunrise   *int   `json:"sunrise"`
	Sunset    *int   `json:"sunset"`
	CivilDawn *int   `json:"civilDawn"`
	CivilDusk *int   `json:"civilDusk"`
	Available bool   `json:"available"`
}

// minutesInDay bounds a valid minute-of-day value (0..1439).
const minutesInDay = 24 * 60

func newSunContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/sun")
	return c, rec
}

func decodeSun(t *testing.T, rec *httptest.ResponseRecorder) sunJSON {
	t.Helper()
	var resp sunJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

func TestGetAnalyticsSun_SingleDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	// Helsinki always has a sunrise and a sunset on a spring date, so events are defined.
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	c, rec := newSunContext(e, "/api/v2/analytics/sun?date=2026-03-20")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.Equal(t, "2026-03-20", resp.Date)
	assert.True(t, resp.Available)
	require.NotNil(t, resp.Sunrise)
	require.NotNil(t, resp.Sunset)
	// Events are minute-of-day in local time, and on a spring day sunrise precedes sunset.
	assert.GreaterOrEqual(t, *resp.Sunrise, 0)
	assert.Less(t, *resp.Sunset, minutesInDay)
	assert.Less(t, *resp.Sunrise, *resp.Sunset)
	// A genuine civil dawn precedes sunrise; a genuine civil dusk follows sunset.
	require.NotNil(t, resp.CivilDawn)
	require.NotNil(t, resp.CivilDusk)
	assert.Less(t, *resp.CivilDawn, *resp.Sunrise)
	assert.Greater(t, *resp.CivilDusk, *resp.Sunset)
}

func TestGetAnalyticsSun_RangeUsesMidpoint(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	// 2026-03-01 .. 2026-03-31 -> calendar midpoint 2026-03-16. The midpoint is computed by
	// calendar-day arithmetic so it is stable across the end-of-March DST transition.
	c, rec := newSunContext(e, "/api/v2/analytics/sun?start_date=2026-03-01&end_date=2026-03-31")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.Equal(t, "2026-03-16", resp.Date)
	assert.True(t, resp.Available)
	require.NotNil(t, resp.Sunrise)
	require.NotNil(t, resp.Sunset)
}

func TestGetAnalyticsSun_LoneStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	// With only start_date, that date is used directly (no range to collapse).
	c, rec := newSunContext(e, "/api/v2/analytics/sun?start_date=2026-03-20")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "2026-03-20", decodeSun(t, rec).Date)
}

func TestGetAnalyticsSun_PolarDayUnavailable(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	// Ny-Ålesund, Svalbard (78.9N): on the summer solstice the sun never sets, so SunCalc cannot
	// compute sunrise/sunset and the endpoint reports the day as unavailable rather than erroring.
	controller.SunCalc = suncalc.NewSunCalc(78.9, 11.9)

	c, rec := newSunContext(e, "/api/v2/analytics/sun?date=2026-06-21")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.Equal(t, "2026-06-21", resp.Date)
	assert.False(t, resp.Available)
	assert.Nil(t, resp.Sunrise)
	assert.Nil(t, resp.Sunset)
	assert.Nil(t, resp.CivilDawn)
	assert.Nil(t, resp.CivilDusk)
}

func TestGetAnalyticsSun_WhiteNightOmitsCivilTwilight(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	// At ~61N on the summer solstice the sun still rises and sets, but never descends to civil
	// twilight (white nights), so SunCalc substitutes sunrise/sunset for civil dawn/dusk. The
	// handler must report available:true with sunrise/sunset set but civilDawn/civilDusk nil
	// (not the sunrise/sunset fallback values masquerading as a twilight band).
	controller.SunCalc = suncalc.NewSunCalc(61.0, 24.0)

	c, rec := newSunContext(e, "/api/v2/analytics/sun?date=2026-06-21")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.True(t, resp.Available)
	require.NotNil(t, resp.Sunrise)
	require.NotNil(t, resp.Sunset)
	assert.Less(t, *resp.Sunrise, *resp.Sunset)
	assert.Nil(t, resp.CivilDawn, "civil dawn must be omitted when no genuine civil twilight occurs")
	assert.Nil(t, resp.CivilDusk, "civil dusk must be omitted when no genuine civil twilight occurs")
}

func TestGetAnalyticsSun_SunCalcUnavailable(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	// SunCalc not configured: the endpoint degrades gracefully (200, available:false) so the clock
	// still renders its hourly bars without day/night shading.
	controller.SunCalc = nil

	c, rec := newSunContext(e, "/api/v2/analytics/sun?date=2026-03-20")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.Equal(t, "2026-03-20", resp.Date)
	assert.False(t, resp.Available)
	assert.Nil(t, resp.Sunrise)
	assert.Nil(t, resp.Sunset)
}

func TestGetAnalyticsSun_DefaultsToToday(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	// No date params: the handler uses today. Helsinki always has a sunrise/sunset, so available.
	c, rec := newSunContext(e, "/api/v2/analytics/sun")
	require.NoError(t, controller.GetAnalyticsSun(c))
	require.Equal(t, http.StatusOK, rec.Code)

	resp := decodeSun(t, rec)
	assert.NotEmpty(t, resp.Date)
	assert.True(t, resp.Available)
}

func TestGetAnalyticsSun_InvalidDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	c, rec := newSunContext(e, "/api/v2/analytics/sun?date=not-a-date")
	err := controller.GetAnalyticsSun(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAnalyticsSun_InvalidRangeOrder(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)
	controller.SunCalc = suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	c, rec := newSunContext(e, "/api/v2/analytics/sun?start_date=2026-03-31&end_date=2026-03-01")
	err := controller.GetAnalyticsSun(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHourlyDistribution_BackCompatUnchanged guards the API backward-compatibility constraint for
// the nocturnal activity clock (Forgejo #1161): the clock reuses the existing hourly-distribution
// endpoint, which must keep returning a 24-element [{hour, count}] array. This fails if a future
// change alters that shape.
func TestHourlyDistribution_BackCompatUnchanged(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetHourlyDistribution", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return([]datastore.HourlyDistributionData{{Hour: 8, Count: 5}, {Hour: 20, Count: 3}}, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/analytics/time/distribution/hourly?start_date=2026-03-01&end_date=2026-03-02", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/distribution/hourly")

	require.NoError(t, controller.GetTimeOfDayDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []HourlyDistribution
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	// The unchanged shape is one entry per hour of day, ordered 0..23.
	require.Len(t, resp, 24)
	for hour := range resp {
		assert.Equal(t, hour, resp[hour].Hour)
	}
	assert.Equal(t, 5, resp[8].Count)
	assert.Equal(t, 3, resp[20].Count)
	mockDS.AssertExpectations(t)
}
