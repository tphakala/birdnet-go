package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

func TestModelStatsRange(t *testing.T) {
	t.Parallel()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	plus3 := time.FixedZone("UTC+3", 3*3600)

	t.Run("range is parsed in the given zone and the end day is included", func(t *testing.T) {
		t.Parallel()
		start, end, endOut, err := modelStatsRange("2026-03-01", "2026-03-02", plus3)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, plus3).Unix(), start)
		assert.Equal(t, time.Date(2026, 3, 3, 0, 0, 0, 0, plus3).Unix(), end)
		assert.Equal(t, "2026-03-02", endOut)
	})

	t.Run("a DST day is 25 hours long", func(t *testing.T) {
		t.Parallel()
		start, end, _, err := modelStatsRange("2026-10-25", "2026-10-25", helsinki)
		require.NoError(t, err)
		assert.Equal(t, 25*time.Hour, time.Duration(end-start)*time.Second)
	})

	t.Run("the day cap is inclusive", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := modelStatsRange("2025-01-01", "2026-01-01", time.UTC)
		require.NoError(t, err, "366 inclusive days are accepted")
		_, _, _, err = modelStatsRange("2025-01-01", "2026-01-02", time.UTC)
		require.ErrorIs(t, err, ErrModelStatsRangeTooLong)
	})

	t.Run("an omitted end date defaults to start plus the default window", func(t *testing.T) {
		t.Parallel()
		_, _, endOut, err := modelStatsRange("2026-03-01", "", time.UTC)
		require.NoError(t, err)
		assert.Equal(t, "2026-03-31", endOut)
	})

	t.Run("a default end date past year 9999 is computed without reparsing", func(t *testing.T) {
		t.Parallel()
		start, end, _, err := modelStatsRange("9999-12-25", "", time.UTC)
		require.NoError(t, err)
		assert.Greater(t, end, start)
	})

	t.Run("unparseable dates are invalid", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := modelStatsRange("2026-02-30", "", time.UTC)
		require.ErrorIs(t, err, ErrModelStatsInvalidDate)
		_, _, _, err = modelStatsRange("2026-03-01", "2026-02-30", time.UTC)
		require.ErrorIs(t, err, ErrModelStatsInvalidDate)
	})

	t.Run("an end before the start is invalid", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := modelStatsRange("2026-03-10", "2026-03-01", time.UTC)
		require.ErrorIs(t, err, ErrModelStatsInvalidDate)
	})
}

func sampleModelContributionStats() *repository.ModelContributionStats {
	minA, meanA := 0.6, 0.7
	return &repository.ModelContributionStats{
		TotalDetections:             10,
		DetectionsWithContributions: 8,
		TotalHits:                   12,
		Models: []repository.ModelContributionStat{
			{
				ModelID: 1, Name: "BirdNET", Version: "2.4", Variant: "default", ModelType: "bird",
				Detections: 8, Hits: 9, HitShare: 0.75, SoleDetections: 5, DetectionsWithStats: 3,
				MaxConfidence: 0.9, MinConfidence: &minA, MeanConfidence: &meanA,
				Correct: 2, FalsePositive: 1, Unreviewed: 5,
			},
			{
				ModelID: 2, Name: "Perch", Version: "2", Variant: "default", ModelType: "bird",
				Detections: 3, Hits: 3, HitShare: 0.25, SoleDetections: 0,
				MaxConfidence: 0.8, Unreviewed: 3,
			},
		},
		Agreements: []repository.ModelAgreement{{ModelAID: 1, ModelBID: 2, SharedDetections: 3}},
	}
}

func TestGetModelContributionStats_Handler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		query       string
		stats       *repository.ModelContributionStats
		wantStart   string
		wantEnd     string
		wantModels  int
		wantNullKey bool
	}{
		{
			name:        "explicit range",
			query:       "?start_date=2026-03-01&end_date=2026-03-10",
			stats:       sampleModelContributionStats(),
			wantStart:   "2026-03-01",
			wantEnd:     "2026-03-10",
			wantModels:  2,
			wantNullKey: true,
		},
		{
			name:        "end date omitted defaults to start plus 30 days",
			query:       "?start_date=2026-03-01",
			stats:       sampleModelContributionStats(),
			wantStart:   "2026-03-01",
			wantEnd:     "2026-03-31",
			wantModels:  2,
			wantNullKey: true,
		},
		{
			name:       "empty result serializes empty arrays",
			query:      "?start_date=2026-03-01&end_date=2026-03-02",
			stats:      &repository.ModelContributionStats{Models: []repository.ModelContributionStat{}, Agreements: []repository.ModelAgreement{}},
			wantStart:  "2026-03-01",
			wantEnd:    "2026-03-02",
			wantModels: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &mockInsightsRepo{modelContributionStats: tt.stats}
			e, controller := setupInsightsTestController(t, mock)

			req := httptest.NewRequest(http.MethodGet, "/api/v2/insights/model-contributions"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.getModelContributionStatsImpl(e.NewContext(req, rec)))
			require.Equal(t, http.StatusOK, rec.Code)

			// The forwarded range must be the zone-aware conversion of the same strings.
			wantStart, wantEnd, _, err := modelStatsRange(tt.wantStart, tt.wantEnd, time.Local)
			require.NoError(t, err)
			assert.Equal(t, wantStart, mock.lastStart)
			assert.Equal(t, wantEnd, mock.lastEnd)

			var raw map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
			assert.Equal(t, tt.wantStart, raw["start_date"])
			assert.Equal(t, tt.wantEnd, raw["end_date"])
			for _, key := range []string{"total_detections", "detections_with_contributions", "total_hits", "models", "agreements"} {
				assert.Contains(t, raw, key)
			}
			models, ok := raw["models"].([]any)
			require.True(t, ok, "models must serialize as an array, not null")
			assert.Len(t, models, tt.wantModels)
			agreements, ok := raw["agreements"].([]any)
			require.True(t, ok, "agreements must serialize as an array, not null")
			if tt.wantModels == 0 {
				assert.Empty(t, agreements)
			}

			if !tt.wantNullKey {
				return
			}
			first, ok := models[0].(map[string]any)
			require.True(t, ok)
			confidence, ok := first["confidence"].(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, 0.6, confidence["min"], 1e-9)
			assert.InDelta(t, 0.7, confidence["mean"], 1e-9)
			second, ok := models[1].(map[string]any)
			require.True(t, ok)
			confidence, ok = second["confidence"].(map[string]any)
			require.True(t, ok)
			assert.Contains(t, confidence, "min", "null stats keep their key")
			assert.Nil(t, confidence["min"])
			assert.Contains(t, confidence, "mean")
			assert.Nil(t, confidence["mean"])
			outcomes, ok := first["outcomes"].(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, 1, outcomes["false_positive"], 0)
		})
	}
}

func TestGetModelContributionStats_ResponseMapping(t *testing.T) {
	t.Parallel()
	e, controller := setupInsightsTestController(t, &mockInsightsRepo{modelContributionStats: sampleModelContributionStats()})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/insights/model-contributions?start_date=2026-03-01&end_date=2026-03-10", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.getModelContributionStatsImpl(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	assert.JSONEq(t, `{
		"start_date": "2026-03-01",
		"end_date": "2026-03-10",
		"total_detections": 10,
		"detections_with_contributions": 8,
		"total_hits": 12,
		"models": [
			{
				"model_id": 1, "name": "BirdNET", "version": "2.4", "variant": "default", "model_type": "bird",
				"detections": 8, "hits": 9, "hit_share": 0.75, "sole_detections": 5,
				"confidence": {"min": 0.6, "mean": 0.7, "max": 0.9, "detections_with_stats": 3},
				"outcomes": {"correct": 2, "false_positive": 1, "unreviewed": 5}
			},
			{
				"model_id": 2, "name": "Perch", "version": "2", "variant": "default", "model_type": "bird",
				"detections": 3, "hits": 3, "hit_share": 0.25, "sole_detections": 0,
				"confidence": {"min": null, "mean": null, "max": 0.8, "detections_with_stats": 0},
				"outcomes": {"correct": 0, "false_positive": 0, "unreviewed": 3}
			}
		],
		"agreements": [{"model_a_id": 1, "model_b_id": 2, "shared_detections": 3}]
	}`, rec.Body.String())
}

func TestGetModelContributionStats_RejectsBadParams(t *testing.T) {
	t.Parallel()
	e, controller := setupInsightsTestController(t, &mockInsightsRepo{modelContributionStats: sampleModelContributionStats()})
	for _, q := range []string{
		"",
		"?start_date=not-a-date",
		"?start_date=2026-03-10&end_date=2026-03-01",
		"?start_date=2025-01-01&end_date=2026-01-02",
	} {
		t.Run(q, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/insights/model-contributions"+q, http.NoBody)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.getModelContributionStatsImpl(e.NewContext(req, rec)))
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}
