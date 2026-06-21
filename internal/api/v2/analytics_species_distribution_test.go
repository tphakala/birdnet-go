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

// speciesDistributionJSON mirrors the ridgeline wire shape from the design spec (section 6.2).
type speciesDistributionJSON struct {
	ScientificName string      `json:"scientificName"`
	Buckets        [24]float64 `json:"buckets"`
	Total          int         `json:"total"`
}

func sampleSpeciesDistribution() []datastore.SpeciesHourlyDistribution {
	var blackbird, robin [24]float64
	blackbird[6] = 0.75
	blackbird[18] = 0.25
	robin[12] = 1.0
	return []datastore.SpeciesHourlyDistribution{
		{ScientificName: "Turdus merula", Buckets: blackbird, Total: 40},
		{ScientificName: "Erithacus rubecula", Buckets: robin, Total: 12},
	}
}

func newSpeciesDistributionContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/distribution/species")
	return c, rec
}

func TestGetSpeciesHourlyDistribution_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Default limit (no ?limit) is the ridgeline's top-5.
	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", 5).
		Return(sampleSpeciesDistribution(), nil)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []speciesDistributionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)
	assert.Equal(t, "Turdus merula", resp[0].ScientificName)
	assert.Equal(t, 40, resp[0].Total)
	assert.InDelta(t, 0.75, resp[0].Buckets[6], 1e-9)
	assert.InDelta(t, 0.25, resp[0].Buckets[18], 1e-9)
	assert.Equal(t, "Erithacus rubecula", resp[1].ScientificName)
	assert.InDelta(t, 1.0, resp[1].Buckets[12], 1e-9)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesHourlyDistribution_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", 5).
		Return([]datastore.SpeciesHourlyDistribution{}, nil)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely. Assert
	// JSON semantics rather than the raw body to avoid coupling to Echo's newline formatting.
	var resp []speciesDistributionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
}

func TestGetSpeciesHourlyDistribution_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-31", 5).
		Return(sampleSpeciesDistribution(), nil)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesHourlyDistribution_ClampsLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		limitParm string
		wantLimit int
	}{
		{"valid in range passes through", "3", 3},
		{"max allowed passes through", "8", 8},
		{"over max falls back to default", "99", defaultSpeciesRidgelineLimit},
		{"zero falls back to default", "0", defaultSpeciesRidgelineLimit},
		{"non-numeric falls back to default", "abc", defaultSpeciesRidgelineLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)

			mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", tt.wantLimit).
				Return(sampleSpeciesDistribution(), nil)

			c, rec := newSpeciesDistributionContext(e,
				"/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02&limit="+tt.limitParm)
			require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
			require.Equal(t, http.StatusOK, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}

func TestGetSpeciesHourlyDistribution_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species")
	err := controller.GetSpeciesHourlyDistribution(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesHourlyDistribution_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species?start_date=not-a-date")
	err := controller.GetSpeciesHourlyDistribution(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetSpeciesHourlyDistribution_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", 5).
		Return([]datastore.SpeciesHourlyDistribution(nil), context.DeadlineExceeded)

	c, rec := newSpeciesDistributionContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
