// api_defaults_regression_test.go: Regression tests for API endpoints called with
// no query parameters. The config migration side of #2352 (MigrateDashboardLayout
// zeroing Dashboard.SummaryLimit) is covered by TestSettings_MigrateDashboardLayout and
// TestSettings_GetEffectiveSummaryLimit in internal/conf/dashboard_migration_test.go.
//
// Pattern: call endpoint with no/minimal query params → assert datastore receives
// valid fallback values → assert non-empty response.

package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// testNotes is reusable mock data for endpoints returning []datastore.Note.
// ScientificName must be set to prevent aggregation collapse in daily species summary.
func testNotes() []datastore.Note {
	today := time.Now().Format(time.DateOnly)
	return []datastore.Note{
		{
			ID:             1,
			CommonName:     "American Robin",
			ScientificName: sciAmericanRobin,
			Confidence:     0.85,
			Date:           today,
			Time:           "08:30:00",
		},
		{
			ID:             2,
			CommonName:     "Blue Jay",
			ScientificName: sciBlueJay,
			Confidence:     0.72,
			Date:           today,
			Time:           "09:15:00",
		},
	}
}

// executeRequest creates an HTTP request with no query parameters and returns the recorder.
func executeRequest(t *testing.T, e *echo.Echo, method, path string, handler echo.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath(path)

	err := handler(c)
	// Some handlers return ErrResponseHandled when they write the response directly
	if err != nil && !errors.Is(err, ErrResponseHandled) {
		// Handler returned an error - check if it wrote an HTTP error response
		if httpErr, ok := errors.AsType[*echo.HTTPError](err); ok {
			rec.Code = httpErr.Code
		} else {
			// Unexpected non-HTTP error - fail the test immediately
			require.NoError(t, err, "handler returned an unexpected error")
		}
	}
	return rec
}

func TestDailySpeciesSummary_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	today := time.Now().Format(time.DateOnly)
	notes := testNotes()

	// Key assertion: GetTopBirdsData must be called with today's date and the raw limit (0 = no limit)
	mockDS.On("GetTopBirdsData", mock.Anything, today, 0.0, 0).Return(notes, nil).Once()

	// aggregateDailySpeciesData calls GetBatchHourlyOccurrences for hourly counts
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, today, today, mock.Anything, 0.0).
		Return(map[string][24]int{
			sciAmericanRobin: {0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			sciBlueJay:       {0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		}, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/daily", controller.GetDailySpeciesSummary)

	assert.Equal(t, http.StatusOK, rec.Code)
	body := strings.TrimSpace(rec.Body.String())
	assert.NotEqual(t, "[]", body, "response should not be empty when data exists")
	assert.NotEqual(t, "null", body, "response should not be null when data exists")

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return species data with default params")

	mockDS.AssertExpectations(t)
}

func TestNewSpeciesDetections_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockData := []datastore.NewSpeciesData{
		{
			ScientificName: sciAmericanRobin,
			CommonName:     "American Robin",
			FirstSeenDate:  "2025-01-15",
			CountInPeriod:  5,
		},
	}

	mockDS.On("GetNewSpeciesDetections",
		mock.Anything,          // context
		mock.Anything,          // startDate (30 days ago)
		mock.Anything,          // endDate (today)
		defaultNewSpeciesLimit, // 100
		0,                      // offset
	).Return(mockData, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/species/detections/new", controller.GetNewSpeciesDetections)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.NotEmpty(t, result, "should return new species with default params")

	mockDS.AssertExpectations(t)
}

func TestTimeOfDayDistribution_DefaultParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockData := []datastore.HourlyDistributionData{
		{Hour: 8, Count: 5},
		{Hour: 9, Count: 3},
	}

	mockDS.On("GetHourlyDistribution",
		mock.Anything, // context
		mock.Anything, // startDate
		mock.Anything, // endDate
		"",            // species (empty = all)
	).Return(mockData, nil).Once()

	rec := executeRequest(t, e, http.MethodGet, "/api/v2/analytics/time/distribution/hourly", controller.GetTimeOfDayDistribution)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Len(t, result, 24, "should return 24-hour distribution array")

	mockDS.AssertExpectations(t)
}

func TestRequiredParams_ReturnBadRequest(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("issue", "2361")

	e, _, controller := setupAnalyticsTestEnvironment(t)

	tests := []struct {
		name    string
		method  string
		path    string
		handler echo.HandlerFunc
	}{
		{
			name:    "GET /analytics/time/hourly requires date and species",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/time/hourly",
			handler: controller.GetHourlyAnalytics,
		},
		{
			name:    "GET /analytics/time/daily requires start_date",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/time/daily",
			handler: controller.GetDailyAnalytics,
		},
		{
			name:    "GET /analytics/species/daily/batch requires dates",
			method:  http.MethodGet,
			path:    "/api/v2/analytics/species/daily/batch",
			handler: controller.GetBatchDailySpeciesSummary,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := executeRequest(t, e, tt.method, tt.path, tt.handler)

			// Must return 400, not 500 or panic
			assert.Equal(t, http.StatusBadRequest, rec.Code,
				"endpoint should return 400 when called without required params")
		})
	}
}
