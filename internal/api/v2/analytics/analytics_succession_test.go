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

// acousticSuccessionJSON mirrors the streamgraph wire shape (raw hour-of-day counts per species).
type acousticSuccessionJSON struct {
	ScientificName string  `json:"scientificName"`
	Counts         [24]int `json:"counts"`
	Total          int     `json:"total"`
}

func sampleAcousticSuccession() []datastore.SpeciesHourlyCounts {
	var blackbird, robin [24]int
	blackbird[6] = 30
	blackbird[18] = 10
	robin[12] = 12
	return []datastore.SpeciesHourlyCounts{
		{ScientificName: "Turdus merula", Counts: blackbird, Total: 40},
		{ScientificName: "Erithacus rubecula", Counts: robin, Total: 12},
	}
}

func newSuccessionContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/time/succession")
	return c, rec
}

func TestGetAcousticSuccession_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Default limit (no ?limit) is the streamgraph's top-6.
	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 6).
		Return(sampleAcousticSuccession(), nil)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []acousticSuccessionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)
	assert.Equal(t, "Turdus merula", resp[0].ScientificName)
	assert.Equal(t, 40, resp[0].Total)
	assert.Equal(t, 30, resp[0].Counts[6])
	assert.Equal(t, 10, resp[0].Counts[18])
	assert.Equal(t, "Erithacus rubecula", resp[1].ScientificName)
	assert.Equal(t, 12, resp[1].Counts[12])
	mockDS.AssertExpectations(t)
}

func TestGetAcousticSuccession_ForwardsSpeciesFilter(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// A repeated ?species filter is trimmed, empty-filtered, and forwarded to the datastore so the
	// streamgraph narrows to the selection instead of the top-N default.
	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02",
		[]string{"Turdus migratorius", "Turdus merula"}, 6).
		Return(sampleAcousticSuccession(), nil)

	c, rec := newSuccessionContext(e,
		"/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02"+
			"&species=Turdus+migratorius&species=+Turdus+merula+&species=")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetAcousticSuccession_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 6).
		Return([]datastore.SpeciesHourlyCounts{}, nil)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []acousticSuccessionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
}

func TestGetAcousticSuccession_DefaultsEndDate(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// With end_date omitted the handler defaults it to a 30-day window: 2026-03-01 + 30d = 2026-03-31.
	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-31", []string(nil), 6).
		Return(sampleAcousticSuccession(), nil)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetAcousticSuccession_ClampsLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		limitParm string
		wantLimit int
	}{
		{"valid in range passes through", "3", 3},
		{"max allowed passes through", "10", 10},
		{"over max falls back to default", "99", defaultSpeciesSuccessionLimit},
		{"zero falls back to default", "0", defaultSpeciesSuccessionLimit},
		{"non-numeric falls back to default", "abc", defaultSpeciesSuccessionLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)

			mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), tt.wantLimit).
				Return(sampleAcousticSuccession(), nil)

			c, rec := newSuccessionContext(e,
				"/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02&limit="+tt.limitParm)
			require.NoError(t, controller.GetAcousticSuccession(c))
			require.Equal(t, http.StatusOK, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}

func TestGetAcousticSuccession_MissingStartDate(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession")
	err := controller.GetAcousticSuccession(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAcousticSuccession_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession?start_date=not-a-date")
	err := controller.GetAcousticSuccession(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAcousticSuccession_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 6).
		Return([]datastore.SpeciesHourlyCounts(nil), context.DeadlineExceeded)

	c, rec := newSuccessionContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetAcousticSuccession(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}
