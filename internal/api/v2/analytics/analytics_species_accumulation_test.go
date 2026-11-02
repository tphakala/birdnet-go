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

func TestGetSpeciesAccumulation_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-03").
		Return(sampleSpeciesAccumulation(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01&end_date=2026-03-03")
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []speciesAccumulationJSON
	decodeExactJSON(t, rec.Body.Bytes(), &resp)
	// Cumulative is monotonic non-decreasing, the middle day adds no species, and the last
	// point carries the total.
	assert.Equal(t, []speciesAccumulationJSON{
		{Date: "2026-03-01", CumulativeSpecies: 1, NewSpecies: 1},
		{Date: "2026-03-02", CumulativeSpecies: 1, NewSpecies: 0},
		{Date: "2026-03-03", CumulativeSpecies: 3, NewSpecies: 2},
	}, resp)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesAccumulation_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesAccumulation", mock.Anything, "2026-03-01", "2026-03-02").
		Return([]datastore.SpeciesAccumulationPoint{}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/accumulation?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesAccumulation(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []speciesAccumulationJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
	mockDS.AssertExpectations(t)
}
