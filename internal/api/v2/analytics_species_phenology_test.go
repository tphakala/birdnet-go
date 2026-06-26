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

// speciesPhenologyJSON mirrors the arrival/departure phenology wire shape (residency-bar Gantt).
type speciesPhenologyJSON struct {
	ScientificName string `json:"scientificName"`
	FirstSeen      string `json:"firstSeen"`
	LastSeen       string `json:"lastSeen"`
	Count          int    `json:"count"`
}

func sampleSpeciesPhenology() []datastore.SpeciesPhenologyPoint {
	return []datastore.SpeciesPhenologyPoint{
		{ScientificName: "Apus apus", FirstSeen: "2026-03-01", LastSeen: "2026-03-20", Count: 40},
		{ScientificName: "Hirundo rustica", FirstSeen: "2026-03-05", LastSeen: "2026-03-28", Count: 25},
	}
}

func newSpeciesPhenologyContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/phenology")
	return c, rec
}

func TestGetSpeciesPhenology_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// No limit in the query -> the handler uses the default (12).
	mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-31", 12).
		Return(sampleSpeciesPhenology(), nil)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-31")
	require.NoError(t, controller.GetSpeciesPhenology(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []speciesPhenologyJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)
	assert.Equal(t, "Apus apus", resp[0].ScientificName)
	assert.Equal(t, "2026-03-01", resp[0].FirstSeen)
	assert.Equal(t, "2026-03-20", resp[0].LastSeen)
	assert.Equal(t, 40, resp[0].Count)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesPhenology_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-02", 12).
		Return([]datastore.SpeciesPhenologyPoint{}, nil)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesPhenology(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []speciesPhenologyJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesPhenology_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-31", 12).
		Return(sampleSpeciesPhenology(), nil)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01")
	require.NoError(t, controller.GetSpeciesPhenology(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesPhenology_LimitClamping(t *testing.T) {
	t.Parallel()

	t.Run("valid limit passes through", func(t *testing.T) {
		t.Parallel()
		e, mockDS, controller := setupAnalyticsTestEnvironment(t)
		mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-02", 5).
			Return(sampleSpeciesPhenology(), nil)

		c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02&limit=5")
		require.NoError(t, controller.GetSpeciesPhenology(c))
		require.Equal(t, http.StatusOK, rec.Code)
		mockDS.AssertExpectations(t)
	})

	t.Run("over-max limit falls back to the default", func(t *testing.T) {
		t.Parallel()
		e, mockDS, controller := setupAnalyticsTestEnvironment(t)
		// 999 exceeds the max (20), so apicore.ParsePaginationLimit returns the default (12).
		mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-02", 12).
			Return(sampleSpeciesPhenology(), nil)

		c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02&limit=999")
		require.NoError(t, controller.GetSpeciesPhenology(c))
		require.Equal(t, http.StatusOK, rec.Code)
		mockDS.AssertExpectations(t)
	})
}

func TestGetSpeciesPhenology_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology")
	err := controller.GetSpeciesPhenology(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesPhenology_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=not-a-date")
	err := controller.GetSpeciesPhenology(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesPhenology_ReversedRange(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesPhenologyContext(e,
		"/api/v2/analytics/species/phenology?start_date=2026-03-05&end_date=2026-03-01")
	err := controller.GetSpeciesPhenology(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesPhenology_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-02", 12).
		Return([]datastore.SpeciesPhenologyPoint(nil), context.DeadlineExceeded)

	c, rec := newSpeciesPhenologyContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetSpeciesPhenology(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
