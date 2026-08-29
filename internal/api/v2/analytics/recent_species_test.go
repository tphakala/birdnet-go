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

	notesForFilter := func(filters *datastore.AdvancedSearchFilters) []datastore.Note {
		filtered := make([]datastore.Note, 0, len(notes))
		startDate := filters.DateRange.Start.Format(time.DateOnly)
		endDate := filters.DateRange.End.Format(time.DateOnly)
		for _, note := range notes {
			if note.Date >= startDate && note.Date <= endDate {
				filtered = append(filtered, note)
			}
		}
		return filtered
	}

	mockDS.On("SearchNotesAdvanced", mock.MatchedBy(func(filters *datastore.AdvancedSearchFilters) bool {
		return filters != nil &&
			filters.DateRange != nil &&
			filters.DateRange.Start.Format(time.DateOnly) == filters.DateRange.End.Format(time.DateOnly) &&
			filters.SortBy == recentSpeciesSortByDateDesc &&
			filters.Limit == recentSpeciesCandidateLimit(2) &&
			filters.Confidence == nil
	})).Return(
		func(filters *datastore.AdvancedSearchFilters) []datastore.Note {
			return notesForFilter(filters)
		},
		func(filters *datastore.AdvancedSearchFilters) int64 {
			return int64(len(notesForFilter(filters)))
		},
		nil,
	)

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

func TestRecentSpeciesSearchDateRangesSpansMidnight(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("feature", "recent-species-activity")
	t.Attr("type", "unit")

	loc := time.FixedZone("test", 0)
	since := time.Date(2026, 5, 1, 22, 0, 0, 0, loc)
	now := time.Date(2026, 5, 2, 2, 0, 0, 0, loc)

	ranges := recentSpeciesSearchDateRanges(since, now)

	require.Len(t, ranges, 2)
	assert.Equal(t, "2026-05-02", ranges[0].Start.Format(time.DateOnly))
	assert.Equal(t, ranges[0].Start, ranges[0].End)
	assert.Equal(t, "2026-05-01", ranges[1].Start.Format(time.DateOnly))
	assert.Equal(t, ranges[1].Start, ranges[1].End)
}
