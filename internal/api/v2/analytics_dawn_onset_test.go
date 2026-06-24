package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// dawnOnsetJSON mirrors the dawn-chorus onset wire shape. OnsetRelMinutes is a pointer so a null
// day (too few detections / no civil dawn) round-trips as JSON null rather than 0.
type dawnOnsetJSON struct {
	Date            string `json:"date"`
	OnsetRelMinutes *int   `json:"onsetRelMinutes"`
	DetectionCount  int    `json:"detectionCount"`
}

func sampleDawnOnset() []datastore.DailyActivityOnset {
	return []datastore.DailyActivityOnset{
		{Date: "2026-03-01", OnsetRelMinutes: new(20), DetectionCount: 42},
		// A day below the min-count / with no civil dawn: nil onset, rendered as a gap by the client.
		{Date: "2026-03-02", OnsetRelMinutes: nil, DetectionCount: 3},
	}
}

func newDawnOnsetContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/dawn-onset")
	return c, rec
}

func TestGetDawnChorusOnset_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleDawnOnset(), nil)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []dawnOnsetJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)

	assert.Equal(t, "2026-03-01", resp[0].Date)
	require.NotNil(t, resp[0].OnsetRelMinutes)
	assert.Equal(t, 20, *resp[0].OnsetRelMinutes)
	assert.Equal(t, 42, resp[0].DetectionCount)

	assert.Equal(t, "2026-03-02", resp[1].Date)
	assert.Nil(t, resp[1].OnsetRelMinutes)
	assert.Equal(t, 3, resp[1].DetectionCount)
	mockDS.AssertExpectations(t)
}

func TestGetDawnChorusOnset_NullOnsetSerialisesAsNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleDawnOnset(), nil)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// A gap day must serialise as null (not 0 or omitted) so the client breaks its trend line there.
	assert.Contains(t, rec.Body.String(), `"onsetRelMinutes":null`)
}

func TestGetDawnChorusOnset_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return([]datastore.DailyActivityOnset{}, nil)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]\n", rec.Body.String())
}

func TestGetDawnChorusOnset_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted, the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-31", "").
		Return(sampleDawnOnset(), nil)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetDawnChorusOnset_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset")
	err := controller.GetDawnChorusOnset(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetDawnChorusOnset_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=not-a-date")
	err := controller.GetDawnChorusOnset(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetDawnChorusOnset_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return([]datastore.DailyActivityOnset{}, context.DeadlineExceeded)

	c, rec := newDawnOnsetContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetDawnChorusOnset(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
