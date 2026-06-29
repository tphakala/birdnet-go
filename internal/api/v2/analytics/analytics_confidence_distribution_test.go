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

// confidenceDistributionJSON mirrors the confidence-distribution wire shape (design spec section 6.5).
type confidenceDistributionJSON struct {
	ScientificName string    `json:"scientificName"`
	Bins           []float64 `json:"bins"`
	Total          int       `json:"total"`
}

func sampleConfidenceDistribution() []datastore.SpeciesConfidenceHistogram {
	return []datastore.SpeciesConfidenceHistogram{
		{ScientificName: "Turdus merula", Bins: []float64{0.1, 0.2, 0.3, 0.4}, Total: 40},
		{ScientificName: "Erithacus rubecula", Bins: []float64{0.25, 0.25, 0.25, 0.25}, Total: 12},
	}
}

func newConfidenceDistributionContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/confidence/distribution")
	return c, rec
}

func TestGetConfidenceDistribution_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Defaults: no species filter ("" passed through), bins 20, top-5 limit.
	mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-02", "", 20, 5).
		Return(sampleConfidenceDistribution(), nil)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetConfidenceDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []confidenceDistributionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)
	assert.Equal(t, "Turdus merula", resp[0].ScientificName)
	assert.Equal(t, 40, resp[0].Total)
	require.Len(t, resp[0].Bins, 4)
	assert.InDelta(t, 0.4, resp[0].Bins[3], 1e-9)
	assert.Equal(t, "Erithacus rubecula", resp[1].ScientificName)
	mockDS.AssertExpectations(t)
}

func TestGetConfidenceDistribution_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-02", "", 20, 5).
		Return([]datastore.SpeciesConfidenceHistogram{}, nil)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetConfidenceDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []confidenceDistributionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
	mockDS.AssertExpectations(t)
}

func TestGetConfidenceDistribution_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-31", "", 20, 5).
		Return(sampleConfidenceDistribution(), nil)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution?start_date=2026-03-01")
	require.NoError(t, controller.GetConfidenceDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetConfidenceDistribution_ClampsBins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		binsParm string
		wantBins int
	}{
		{"valid in range passes through", "30", 30},
		{"below min clamps up", "3", minConfidenceBins},
		{"above max clamps down", "999", maxConfidenceBins},
		{"non-numeric falls back to default", "abc", defaultConfidenceBins},
		{"empty falls back to default", "", defaultConfidenceBins},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)

			mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-02", "", tt.wantBins, 5).
				Return(sampleConfidenceDistribution(), nil)

			target := "/api/v2/analytics/confidence/distribution?start_date=2026-03-01&end_date=2026-03-02"
			if tt.binsParm != "" {
				target += "&bins=" + tt.binsParm
			}
			c, rec := newConfidenceDistributionContext(e, target)
			require.NoError(t, controller.GetConfidenceDistribution(c))
			require.Equal(t, http.StatusOK, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}

func TestGetConfidenceDistribution_ClampsLimit(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)

			mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-02", "", 20, tt.wantLimit).
				Return(sampleConfidenceDistribution(), nil)

			c, rec := newConfidenceDistributionContext(e,
				"/api/v2/analytics/confidence/distribution?start_date=2026-03-01&end_date=2026-03-02&limit="+tt.limitParm)
			require.NoError(t, controller.GetConfidenceDistribution(c))
			require.Equal(t, http.StatusOK, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}

func TestGetConfidenceDistribution_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution")
	err := controller.GetConfidenceDistribution(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetConfidenceDistribution_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution?start_date=not-a-date")
	err := controller.GetConfidenceDistribution(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetConfidenceDistribution_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetConfidenceHistogram", mock.Anything, "2026-03-01", "2026-03-02", "", 20, 5).
		Return([]datastore.SpeciesConfidenceHistogram(nil), context.DeadlineExceeded)

	c, rec := newConfidenceDistributionContext(e, "/api/v2/analytics/confidence/distribution?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetConfidenceDistribution(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
