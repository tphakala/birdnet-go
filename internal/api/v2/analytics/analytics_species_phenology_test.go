package analytics

import (
	"encoding/json"
	"net/http"
	"testing"

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

func TestGetSpeciesPhenology_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// No limit in the query -> the handler uses the default (12).
	mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-31", 12).
		Return(sampleSpeciesPhenology(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-31")
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

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesPhenology(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []speciesPhenologyJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesPhenology_LimitClamping(t *testing.T) {
	t.Parallel()

	t.Run("valid limit passes through", func(t *testing.T) {
		t.Parallel()
		e, mockDS, controller := setupAnalyticsTestEnvironment(t)
		mockDS.On("GetSpeciesPhenology", mock.Anything, "2026-03-01", "2026-03-02", 5).
			Return(sampleSpeciesPhenology(), nil)

		c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02&limit=5")
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

		c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/phenology?start_date=2026-03-01&end_date=2026-03-02&limit=999")
		require.NoError(t, controller.GetSpeciesPhenology(c))
		require.Equal(t, http.StatusOK, rec.Code)
		mockDS.AssertExpectations(t)
	})
}
