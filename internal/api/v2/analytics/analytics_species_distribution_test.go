package analytics

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// speciesDistributionJSON mirrors the ridgeline wire shape from the design spec (section 6.2).
// Buckets is a slice, not an array, so a response with a different number of hour buckets
// decodes to a different value instead of being truncated or zero padded.
type speciesDistributionJSON struct {
	ScientificName string    `json:"scientificName"`
	CommonName     string    `json:"commonName"`
	Buckets        []float64 `json:"buckets"`
	Total          int       `json:"total"`
}

func sampleSpeciesDistribution() []datastore.SpeciesHourlyDistribution {
	var blackbird, robin [24]float64
	blackbird[6] = 0.75
	blackbird[18] = 0.25
	robin[12] = 1.0
	return []datastore.SpeciesHourlyDistribution{
		{ScientificName: "Turdus merula", CommonName: "Eurasian Blackbird", Buckets: blackbird, Total: 40},
		{ScientificName: "Erithacus rubecula", CommonName: "European Robin", Buckets: robin, Total: 12},
	}
}

func TestGetSpeciesHourlyDistribution_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Default limit (no ?limit) is the ridgeline's top-5.
	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 5).
		Return(sampleSpeciesDistribution(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []speciesDistributionJSON
	decodeExactJSON(t, rec.Body.Bytes(), &resp)
	// The response carries every row of the fixture with all 24 hour buckets, in order.
	rows := sampleSpeciesDistribution()
	want := make([]speciesDistributionJSON, 0, len(rows))
	for _, row := range rows {
		want = append(want, speciesDistributionJSON{ScientificName: row.ScientificName, CommonName: row.CommonName, Buckets: row.Buckets[:], Total: row.Total})
	}
	require.Len(t, want, 2)
	assert.Equal(t, want, resp)
	mockDS.AssertExpectations(t)
}

// TestGetSpeciesHourlyDistribution_ForwardsSpeciesFilter verifies the ridgeline endpoint parses the
// repeated ?species query param (trimming, dropping empties, and collapsing case-insensitive
// duplicates) and forwards the resulting scientific-name filter to the datastore.
func TestGetSpeciesHourlyDistribution_ForwardsSpeciesFilter(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// A repeated ?species filter is trimmed, empty-filtered, case-insensitively de-duplicated, and
	// forwarded to the datastore so the ridgeline narrows to the selection instead of the top-N
	// default. The trailing "turdus migratorius" is a case-variant duplicate that must collapse.
	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02",
		[]string{"Turdus migratorius", "Turdus merula"}, 5).
		Return(sampleSpeciesDistribution(), nil)

	c, rec := newAnalyticsContext(e,
		"/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02"+
			"&species=Turdus+migratorius&species=+Turdus+merula+&species=&species=turdus+migratorius")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

// TestGetSpeciesHourlyDistribution_RejectsOversizedSpeciesFilter verifies the repeated ?species
// param is bounded. The datastore drops its row limit once a species filter is present, so `limit`
// no longer caps this path and the selection itself is the only bound; an unbounded filter would let
// a client dictate the IN-list size, the row count and the response size. The cap matches the batch
// time-of-day endpoints (maxSpeciesBatch) and the control bar's MAX_SPECIES, so a legitimate UI
// selection always fits. The datastore must not be reached at all when the guard trips.
func TestGetSpeciesHourlyDistribution_RejectsOversizedSpeciesFilter(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	query := "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02"
	for i := range maxSpeciesBatch + 1 {
		query += fmt.Sprintf("&species=Species+%d", i)
	}

	c, rec := newAnalyticsContext(e, query)
	// The guard writes the 400 itself and reports it with the package's ErrResponseHandled sentinel,
	// matching every other validation helper in this handler.
	err := controller.GetSpeciesHourlyDistribution(c)
	require.ErrorIs(t, err, ErrResponseHandled)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	mockDS.AssertNotCalled(t, "GetHourlyDistributionBySpecies")
}

// TestGetSpeciesHourlyDistribution_AllowsMaxSpeciesFilter pins the boundary: exactly maxSpeciesBatch
// species is accepted and forwarded, so the guard above cannot drift into rejecting a full selection.
func TestGetSpeciesHourlyDistribution_AllowsMaxSpeciesFilter(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	query := "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02"
	want := make([]string, 0, maxSpeciesBatch)
	for i := range maxSpeciesBatch {
		query += fmt.Sprintf("&species=Species+%d", i)
		want = append(want, fmt.Sprintf("Species %d", i))
	}

	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02",
		want, defaultSpeciesRidgelineLimit).
		Return(sampleSpeciesDistribution(), nil)

	c, rec := newAnalyticsContext(e, query)
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetSpeciesHourlyDistribution_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetHourlyDistributionBySpecies", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 5).
		Return([]datastore.SpeciesHourlyDistribution{}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely. Assert
	// JSON semantics rather than the raw body to avoid coupling to Echo's newline formatting.
	var resp []speciesDistributionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
}

func TestGetSpeciesHourlyDistribution_ClampsLimit(t *testing.T) {
	t.Parallel()

	tests := []LimitClampTestCase{
		{"valid in range passes through", "3", 3},
		{"max allowed passes through", "10", 10},
		{"over max falls back to default", "99", defaultSpeciesRidgelineLimit},
		{"zero falls back to default", "0", defaultSpeciesRidgelineLimit},
		{"non-numeric falls back to default", "abc", defaultSpeciesRidgelineLimit},
	}

	runLimitClampTests(t, tests, func(t *testing.T, tc LimitClampTestCase) {
		t.Helper()

		e, mockDS, controller := setupAnalyticsTestEnvironment(t)

		mockDS.On("GetHourlyDistributionBySpecies",
			mock.Anything,
			"2026-03-01",
			"2026-03-02",
			[]string(nil),
			tc.WantLimit,
		).Return(sampleSpeciesDistribution(), nil)

		c, rec := newAnalyticsContext(
			e,
			"/api/v2/analytics/time/distribution/species?start_date=2026-03-01&end_date=2026-03-02&limit="+tc.LimitParm,
		)

		require.NoError(t, controller.GetSpeciesHourlyDistribution(c))
		require.Equal(t, http.StatusOK, rec.Code)
		mockDS.AssertExpectations(t)
	})
}
