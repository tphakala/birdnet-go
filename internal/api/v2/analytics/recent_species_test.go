package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

func TestGetRecentSpeciesActivity(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "recent-species-activity")
	t.Attr("type", "unit")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)
	now := time.Date(2026, 5, 2, 2, 0, 0, 0, time.Local)
	controller.now = func() time.Time { return now }

	rows := []datastore.RecentSpeciesData{
		{
			ScientificName:      "Turdus migratorius",
			CommonName:          "American Robin",
			SpeciesCode:         "amerob",
			Bucket:              2,
			Count:               1,
			ConfidenceTotal:     0.66,
			BucketMaxConfidence: 0.66,
		},
		{
			ScientificName:      "Turdus migratorius",
			CommonName:          "American Robin",
			SpeciesCode:         "amerob",
			Bucket:              3,
			Count:               1,
			ConfidenceTotal:     0.82,
			BucketMaxConfidence: 0.82,
			LatestDetectedAt:    now.Add(-10 * time.Minute),
			LatestConfidence:    0.82,
			LatestDetectionID:   2,
		},
		{
			ScientificName:      "Cardinalis cardinalis", //nolint:misspell // Cardinalis is a valid scientific genus name.
			CommonName:          "Northern Cardinal",
			SpeciesCode:         "norcar",
			Bucket:              3,
			Count:               2,
			ConfidenceTotal:     1.65,
			BucketMaxConfidence: 0.95,
			LatestDetectedAt:    now.Add(-20 * time.Minute),
			LatestConfidence:    0.95,
			LatestDetectionID:   4,
		},
	}
	mockDS.On(
		"GetRecentSpeciesData",
		mock.MatchedBy(func(ctx context.Context) bool {
			_, hasDeadline := ctx.Deadline()
			return hasDeadline
		}),
		now.Add(-4*time.Hour),
		now,
		0.0,
		4,
	).Return(rows, nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/recent?hours=4&limit=2&buckets=4", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/species/recent")

	require.NoError(t, controller.GetRecentSpeciesActivity(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []RecentSpeciesActivity
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response, 2)

	assert.Equal(t, "Northern Cardinal", response[0].CommonName)
	assert.Equal(t, uint(4), response[0].LatestDetectionID)
	assert.Equal(t, []float64{0, 0, 0, 0.95}, response[0].ConfidenceTrend)
	assert.Equal(t, "American Robin", response[1].CommonName)
	assert.Equal(t, 2, response[1].Count)
	assert.Equal(t, []float64{0, 0, 0.66, 0.82}, response[1].ConfidenceTrend)
	assert.Equal(t, now.Add(-4*time.Hour).Format(time.RFC3339), response[1].TrendStart)

	mockDS.AssertExpectations(t)
}

func TestGetRecentSpeciesActivityPassesMinimumConfidence(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "recent-species-activity")
	t.Attr("type", "unit")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)
	now := time.Date(2026, 5, 2, 2, 0, 0, 0, time.Local)
	controller.now = func() time.Time { return now }
	mockDS.On(
		"GetRecentSpeciesData",
		mock.Anything,
		now.Add(-4*time.Hour),
		now,
		0.75,
		16,
	).Return([]datastore.RecentSpeciesData{}, nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/recent?min_confidence=75", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/species/recent")

	require.NoError(t, controller.GetRecentSpeciesActivity(ctx))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `[]`, rec.Body.String())
	mockDS.AssertExpectations(t)
}

func TestParseRecentSpeciesActivityParamsClampsBounds(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "recent-species-activity")
	t.Attr("type", "unit")

	tests := []struct {
		name  string
		query string
		want  recentSpeciesActivityParams
	}{
		{
			name:  "upper bounds",
			query: "?hours=999&limit=999&buckets=999&min_confidence=250",
			want: recentSpeciesActivityParams{
				Hours: 24, Limit: 20, Buckets: 96, MinConfidence: 1,
			},
		},
		{
			name:  "lower bounds",
			query: "?hours=1&limit=1&buckets=1&min_confidence=-20",
			want: recentSpeciesActivityParams{
				Hours: 1, Limit: 1, Buckets: 4, MinConfidence: 0,
			},
		},
		{
			name:  "invalid values use defaults",
			query: "?hours=0&limit=nope&buckets=-1&min_confidence=NaN",
			want: recentSpeciesActivityParams{
				Hours: 4, Limit: 8, Buckets: 16, MinConfidence: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, controller := setupAnalyticsTestEnvironment(t)
			req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/recent"+tt.query, http.NoBody)
			ctx := e.NewContext(req, httptest.NewRecorder())
			assert.Equal(t, tt.want, controller.parseRecentSpeciesActivityParams(ctx))
		})
	}
}
