package analytics

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestGetSpeciesDiversity_ResponseShape pins the diversity wire format: the echoed date range, one
// {date, unique_species} row per datastore row in order, and max_diversity as the largest daily
// count. The busiest day is in the middle, so a max that kept the first or last value would fail.
func TestGetSpeciesDiversity_ResponseShape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetSpeciesDiversityData", mock.Anything, "2024-01-15", "2024-01-17").
		Return([]datastore.DailyAnalyticsData{
			{Date: "2024-01-15", Count: 3},
			{Date: "2024-01-16", Count: 5},
			{Date: "2024-01-17", Count: 2},
		}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/species/diversity?start_date=2024-01-15&end_date=2024-01-17")

	require.NoError(t, controller.GetSpeciesDiversity(c))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{
		"start_date": "2024-01-15",
		"end_date": "2024-01-17",
		"data": [
			{"date": "2024-01-15", "unique_species": 3},
			{"date": "2024-01-16", "unique_species": 5},
			{"date": "2024-01-17", "unique_species": 2}
		],
		"max_diversity": 5
	}`, rec.Body.String())
	mockDS.AssertExpectations(t)
}
