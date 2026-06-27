package analytics

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

// yearOverYearPointJSON mirrors one point on the year-over-year wire shape.
type yearOverYearPointJSON struct {
	Date     string `json:"date"`
	MonthDay string `json:"monthDay"`
	ThisYear int    `json:"thisYear"`
	LastYear int    `json:"lastYear"`
	Delta    int    `json:"delta"`
}

// yearOverYearJSON mirrors the year-over-year wire shape: year labels at the root plus a points array.
type yearOverYearJSON struct {
	CurrentYear  int                     `json:"currentYear"`
	PreviousYear int                     `json:"previousYear"`
	Points       []yearOverYearPointJSON `json:"points"`
}

func sampleYearOverYear() datastore.YearOverYearResult {
	return datastore.YearOverYearResult{
		CurrentYear:  2026,
		PreviousYear: 2025,
		Points: []datastore.YearOverYearPoint{
			{Date: "2026-01-01", MonthDay: "01-01", ThisYear: 2, LastYear: 1, Delta: 1},
			{Date: "2026-01-02", MonthDay: "01-02", ThisYear: 3, LastYear: 5, Delta: -2},
		},
	}
}

func newYearOverYearContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/year-over-year")
	return c, rec
}

func TestGetYearOverYear_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetYearOverYear", mock.Anything, "2026-06-23").
		Return(sampleYearOverYear(), nil)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=2026-06-23")
	require.NoError(t, controller.GetYearOverYear(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp yearOverYearJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 2026, resp.CurrentYear)
	assert.Equal(t, 2025, resp.PreviousYear)
	require.Len(t, resp.Points, 2)
	assert.Equal(t, "2026-01-01", resp.Points[0].Date)
	assert.Equal(t, "01-01", resp.Points[0].MonthDay)
	assert.Equal(t, 2, resp.Points[0].ThisYear)
	assert.Equal(t, 1, resp.Points[0].LastYear)
	assert.Equal(t, 1, resp.Points[0].Delta)
	assert.Equal(t, -2, resp.Points[1].Delta)
	mockDS.AssertExpectations(t)
}

func TestGetYearOverYear_EmptyPointsNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetYearOverYear", mock.Anything, "2026-01-01").
		Return(datastore.YearOverYearResult{CurrentYear: 2026, PreviousYear: 2025, Points: []datastore.YearOverYearPoint{}}, nil)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=2026-01-01")
	require.NoError(t, controller.GetYearOverYear(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// points must serialize as [] (not null) so the client can read .length safely.
	var resp yearOverYearJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Points)
	assert.Empty(t, resp.Points)
	mockDS.AssertExpectations(t)
}

func TestGetYearOverYear_DefaultsDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With date omitted the handler passes an empty string through; the datastore resolves "today".
	mockDS.On("GetYearOverYear", mock.Anything, "").
		Return(sampleYearOverYear(), nil)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year")
	require.NoError(t, controller.GetYearOverYear(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetYearOverYear_FutureDatePassedThrough(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// A well-formed future date is not rejected at the handler; the datastore just yields a flat tail.
	mockDS.On("GetYearOverYear", mock.Anything, "2099-12-31").
		Return(sampleYearOverYear(), nil)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=2099-12-31")
	require.NoError(t, controller.GetYearOverYear(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetYearOverYear_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=not-a-date")
	err := controller.GetYearOverYear(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetYearOverYear_InvalidCalendarDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	// A regex-valid but non-existent calendar date must be rejected at the handler with a 400,
	// not fall through to the datastore and surface as a 500.
	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=2026-13-45")
	err := controller.GetYearOverYear(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetYearOverYear_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetYearOverYear", mock.Anything, "2026-06-23").
		Return(datastore.YearOverYearResult{}, context.DeadlineExceeded)

	c, rec := newYearOverYearContext(e, "/api/v2/analytics/time/year-over-year?date=2026-06-23")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetYearOverYear(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
