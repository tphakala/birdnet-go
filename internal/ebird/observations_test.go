package ebird

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRecentObservations(t *testing.T) {
	mockObs := []Observation{
		{
			SpeciesCode:    "eurblk",
			CommonName:     "Eurasian Blackbird",
			ScientificName: "Turdus merula",
			LocationName:   "Test Park",
			ObservationDt:  "2026-03-08 10:30",
			Latitude:       60.17,
			Longitude:      24.94,
			HowMany:        2,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.URL.Path, "/v2/data/obs/geo/recent")
		assert.Equal(t, "test-key", r.Header.Get("X-eBirdApiToken"))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockObs)
	}))
	defer server.Close()

	client := setupTestClient(t, server)
	ctx := t.Context()

	// First call - cache miss, hits mock server
	results, err := client.GetRecentObservations(ctx, 60.17, 24.94, 14)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Turdus merula", results[0].ScientificName)
	assert.Equal(t, "Eurasian Blackbird", results[0].CommonName)
	assert.Equal(t, 2, results[0].HowMany)

	// Second call - should hit cache
	results2, err := client.GetRecentObservations(ctx, 60.17, 24.94, 14)
	require.NoError(t, err)
	assert.Equal(t, results, results2)
}

func TestGetRecentObservations_DefaultDays(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.RawQuery, "back=14")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	client := setupTestClient(t, server)

	// days=0 should default to 14
	results, err := client.GetRecentObservations(t.Context(), 60.17, 24.94, 0)
	require.NoError(t, err)
	assert.Empty(t, results)
}
