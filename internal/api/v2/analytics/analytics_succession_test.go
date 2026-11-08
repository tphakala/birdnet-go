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

// acousticSuccessionJSON mirrors the streamgraph wire shape (raw hour-of-day counts per species).
// Counts is a slice, not an array, so a response with a different number of hour buckets
// decodes to a different value instead of being truncated or zero padded.
type acousticSuccessionJSON struct {
	ScientificName string `json:"scientificName"`
	CommonName     string `json:"commonName"`
	Counts         []int  `json:"counts"`
	Total          int    `json:"total"`
}

func sampleAcousticSuccession() []datastore.SpeciesHourlyCounts {
	var blackbird, robin [24]int
	blackbird[6] = 30
	blackbird[18] = 10
	robin[12] = 12
	return []datastore.SpeciesHourlyCounts{
		{ScientificName: "Turdus merula", CommonName: "Eurasian Blackbird", Counts: blackbird, Total: 40},
		{ScientificName: "Erithacus rubecula", CommonName: "European Robin", Counts: robin, Total: 12},
	}
}

func TestGetAcousticSuccession_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Default limit (no ?limit) is the streamgraph's top-6.
	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 6).
		Return(sampleAcousticSuccession(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []acousticSuccessionJSON
	decodeExactJSON(t, rec.Body.Bytes(), &resp)
	// Every hour of both species is compared, not only the non-zero hours.
	var blackbird, robin [24]int
	blackbird[6] = 30
	blackbird[18] = 10
	robin[12] = 12
	assert.Equal(t, []acousticSuccessionJSON{
		{ScientificName: "Turdus merula", CommonName: "Eurasian Blackbird", Counts: blackbird[:], Total: 40},
		{ScientificName: "Erithacus rubecula", CommonName: "European Robin", Counts: robin[:], Total: 12},
	}, resp)
	mockDS.AssertExpectations(t)
}

func TestGetAcousticSuccession_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetAcousticSuccession", mock.Anything, "2026-03-01", "2026-03-02", []string(nil), 6).
		Return([]datastore.SpeciesHourlyCounts{}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetAcousticSuccession(c))
	require.Equal(t, http.StatusOK, rec.Code)
	// Empty result must serialize as [] (not null) so the client can read .length safely.
	var resp []acousticSuccessionJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp)
	assert.Empty(t, resp)
}

func TestGetAcousticSuccession_ClampsLimit(t *testing.T) {
	t.Parallel()

	tests := []LimitClampTestCase{
		{"valid in range passes through", "3", 3},
		{"max allowed passes through", "10", 10},
		{"over max falls back to default", "99", defaultSpeciesSuccessionLimit},
		{"zero falls back to default", "0", defaultSpeciesSuccessionLimit},
		{"non-numeric falls back to default", "abc", defaultSpeciesSuccessionLimit},
	}

	runLimitClampTests(t, tests, func(t *testing.T, tc LimitClampTestCase) {
		t.Helper()

		e, mockDS, controller := setupAnalyticsTestEnvironment(t)

		mockDS.On("GetAcousticSuccession",
			mock.Anything,
			"2026-03-01",
			"2026-03-02",
			[]string(nil),
			tc.WantLimit,
		).Return(sampleAcousticSuccession(), nil)

		c, rec := newAnalyticsContext(
			e,
			"/api/v2/analytics/time/succession?start_date=2026-03-01&end_date=2026-03-02&limit="+tc.LimitParm,
		)

		require.NoError(t, controller.GetAcousticSuccession(c))
		require.Equal(t, http.StatusOK, rec.Code)
		mockDS.AssertExpectations(t)
	})
}
