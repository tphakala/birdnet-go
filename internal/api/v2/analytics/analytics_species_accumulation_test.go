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

// speciesAccumulationJSON mirrors the species-accumulation wire shape (biodiversity collector's curve).
type speciesAccumulationJSON struct {
	Date              string `json:"date"`
	CumulativeSpecies int    `json:"cumulativeSpecies"`
	NewSpecies        int    `json:"newSpecies"`
}

func sampleSpeciesAccumulation() []datastore.SpeciesAccumulationPoint {
	return []datastore.SpeciesAccumulationPoint{
		{Date: "2026-03-01", CumulativeSpecies: 1, NewSpecies: 1},
		{Date: "2026-03-02", CumulativeSpecies: 1, NewSpecies: 0},
		{Date: "2026-03-03", CumulativeSpecies: 3, NewSpecies: 2},
	}
}

func newSpeciesAccumulationContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/species/accumulation")
	return c, rec
}

func TestGetSpeciesAccumulation_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-03").
		Return(sampleSpeciesAccumulation(), nil)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01&end_date=2026-03-03")
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []speciesAccumulationJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 3)
	assert.Equal(t, "2026-03-01", resp[0].Date)
	assert.Equal(t, 1, resp[0].CumulativeSpecies)
	assert.Equal(t, 1, resp[0].NewSpecies)
	// Cumulative is monotonic non-decreasing; the last point carries the total.
	assert.Equal(t, 3, resp[2].CumulativeSpecies)
	assert.Equal(t, 2, resp[2].NewSpecies)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesAccumulation_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-02").
		Return([]datastore.SpeciesAccumulationPoint{}, nil)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []speciesAccumulationJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesAccumulation_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-31").
		Return(sampleSpeciesAccumulation(), nil)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01")
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesAccumulation_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation")
	err := controller.GetSpeciesAccumulation(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesAccumulation_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation?start_date=not-a-date")
	err := controller.GetSpeciesAccumulation(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesAccumulation_ReversedRange(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesAccumulationContext(e,
		"/api/v2/analytics/species/accumulation?start_date=2026-03-05&end_date=2026-03-01")
	err := controller.GetSpeciesAccumulation(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesAccumulation_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-02").
		Return([]datastore.SpeciesAccumulationPoint(nil), context.DeadlineExceeded)

	c, rec := newSpeciesAccumulationContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
