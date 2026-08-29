package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

	now := time.Now()
	noteAt := func(id uint, detectedAt time.Time, commonName, scientificName string, confidence float64) datastore.Note {
		return datastore.Note{
			ID:             id,
			CommonName:     commonName,
			ScientificName: scientificName,
			SpeciesCode:    strings.ToLower(commonName[:3]),
			Confidence:     confidence,
			Date:           detectedAt.Format(time.DateOnly),
			Time:           detectedAt.Format(time.TimeOnly),
		}
	}

	notes := []datastore.Note{
		noteAt(1, now.Add(-10*time.Minute), "American Robin", "Turdus migratorius", 0.82),
		noteAt(2, now.Add(-2*time.Hour), "American Robin", "Turdus migratorius", 0.66),
		noteAt(3, now.Add(-20*time.Minute), "Northern Cardinal", "Cardinalis cardinalis", 0.95), //nolint:misspell // Scientific name.
		noteAt(4, now.Add(-25*time.Minute), "Northern Cardinal", "Cardinalis cardinalis", 0.70), //nolint:misspell // Scientific name.
		noteAt(5, now.Add(-5*time.Hour), "Blue Jay", "Cyanocitta cristata", 0.99),
	}

	mockDS.On("SearchNotesAdvanced", mock.MatchedBy(func(filters *datastore.AdvancedSearchFilters) bool {
		return filters != nil &&
			filters.DateRange == nil &&
			filters.DetectedAtRange != nil &&
			filters.DetectedAtRange.End.Sub(filters.DetectedAtRange.Start) > 4*time.Hour &&
			filters.Limit == recentSpeciesQueryBatchSize &&
			filters.MinID == 0 &&
			filters.CursorPagination &&
			filters.ExcludeFalsePositives &&
			filters.MinimalResults &&
			filters.SkipTotal &&
			filters.Confidence == nil
	})).Return(notes, int64(0), nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/analytics/species/recent?hours=4&limit=2&buckets=4", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/analytics/species/recent")

	err := controller.GetRecentSpeciesActivity(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response []RecentSpeciesActivity
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response, 2)

	assert.Equal(t, "Northern Cardinal", response[0].CommonName)
	assert.Equal(t, uint(3), response[0].LatestDetectionID)
	assert.Len(t, response[0].ConfidenceTrend, 4)
	assert.InDelta(t, 0.0, response[0].ConfidenceTrend[0], 0.001)
	assert.InDelta(t, 0.95, response[0].ConfidenceTrend[3], 0.001)
	assert.Equal(t, "American Robin", response[1].CommonName)
	assert.Equal(t, 2, response[1].Count)
	assert.NotContains(t, []string{response[0].CommonName, response[1].CommonName}, "Blue Jay")

	mockDS.AssertExpectations(t)
}

func TestLoadRecentSpeciesActivityPaginatesExactWindow(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "recent-species-activity")
	t.Attr("type", "unit")

	loc := time.Local
	since := time.Date(2026, 5, 1, 22, 0, 0, 0, loc)
	now := time.Date(2026, 5, 2, 2, 0, 0, 0, loc)
	_, mockDS, controller := setupAnalyticsTestEnvironment(t)
	firstPage := make([]datastore.Note, recentSpeciesQueryBatchSize)
	for i := range firstPage {
		firstPage[i] = datastore.Note{
			ID:             uint(i + 1),
			Date:           "2026-05-02",
			Time:           "01:30:00",
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			Confidence:     0.8,
		}
	}
	lastPage := []datastore.Note{{
		ID:             uint(recentSpeciesQueryBatchSize + 1),
		Date:           "2026-05-02",
		Time:           "01:30:00",
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.9,
	}}

	baseFilterMatches := func(filters *datastore.AdvancedSearchFilters) bool {
		return filters != nil &&
			filters.DateRange == nil &&
			filters.DetectedAtRange != nil &&
			filters.DetectedAtRange.Start.Equal(since) &&
			filters.DetectedAtRange.End.Equal(now.Add(time.Second)) &&
			filters.Limit == recentSpeciesQueryBatchSize &&
			filters.CursorPagination &&
			filters.ExcludeFalsePositives &&
			filters.MinimalResults &&
			filters.SkipTotal &&
			filters.Confidence != nil &&
			filters.Confidence.Operator == ">=" &&
			filters.Confidence.Value == 0.75
	}
	mockDS.On("SearchNotesAdvanced", mock.MatchedBy(func(filters *datastore.AdvancedSearchFilters) bool {
		return baseFilterMatches(filters) && filters.MinID == 0
	})).Return(firstPage, int64(0), nil).Once()
	mockDS.On("SearchNotesAdvanced", mock.MatchedBy(func(filters *datastore.AdvancedSearchFilters) bool {
		return baseFilterMatches(filters) && filters.MinID == recentSpeciesQueryBatchSize
	})).Return(lastPage, int64(0), nil).Once()

	got, detectionCount, err := controller.loadRecentSpeciesActivity(recentSpeciesActivityParams{
		Hours:         4,
		Limit:         8,
		Buckets:       4,
		MinConfidence: 0.75,
	}, since, now)

	require.NoError(t, err)
	assert.Equal(t, recentSpeciesQueryBatchSize+1, detectionCount)
	require.Len(t, got, 1)
	assert.Equal(t, recentSpeciesQueryBatchSize+1, got[0].Count)
	assert.Equal(t, uint(recentSpeciesQueryBatchSize+1), got[0].LatestDetectionID)
	mockDS.AssertExpectations(t)
}
