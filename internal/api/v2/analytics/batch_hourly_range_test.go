// batch_hourly_range_test.go: handler-level coverage for /analytics/time/hourly/batch.
//
// The endpoint gained a date-RANGE form (start_date/end_date) alongside the original single
// `date` param. The parameter branching and the batch result mapping had no handler-level
// tests, so these pin the request contract and the failure behaviour.

package analytics

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

const (
	batchHourlyPath = "/api/v2/analytics/time/hourly/batch"
	sciBlackbird    = "Turdus merula"
	sciRobin        = "Erithacus rubecula"
)

// batchQuery builds an encoded query string. Scientific names contain spaces, so they must be
// escaped or the request URL does not parse and the handler sees no species at all.
func batchQuery(species []string, kv ...string) string {
	values := url.Values{}
	for _, s := range species {
		values.Add("species", s)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		values.Set(kv[i], kv[i+1])
	}
	return values.Encode()
}

// callBatchHourly runs the handler against an encoded query string and returns the recorder.
func callBatchHourly(t *testing.T, mockDS *mocks.MockInterface, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	controller := newTestHandler(&apicore.Core{DS: mockDS})

	req := httptest.NewRequest(http.MethodGet, batchHourlyPath+"?"+rawQuery, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath(batchHourlyPath)

	// The handler writes its own HTTP response for validation failures, so a nil error here
	// does not imply success; the caller asserts on rec.Code.
	if err := controller.GetBatchHourlySpeciesData(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec
}

// TestGetBatchHourlySpeciesData_RangeParams checks the range form reaches the datastore verbatim,
// with both bounds inclusive. Before the range support the chart could only ask for one day.
func TestGetBatchHourlySpeciesData_RangeParams(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "batch-hourly")

	mockDS := mocks.NewMockInterface(t)
	var hourly [24]int
	hourly[6] = 12

	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2026-03-01", "2026-03-31",
		mock.MatchedBy(func(species []string) bool {
			return len(species) == 1 && species[0] == sciBlackbird
		}), 0.0).
		Return(map[string][24]int{sciBlackbird: hourly}, nil)

	rec := callBatchHourly(t, mockDS, batchQuery([]string{sciBlackbird},
		"start_date", "2026-03-01", "end_date", "2026-03-31", "min_confidence", "0"))

	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string][]HourlyDistribution
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Contains(t, response, sciBlackbird)
	require.Len(t, response[sciBlackbird], 24)
	assert.Equal(t, 12, response[sciBlackbird][6].Count)
	mockDS.AssertExpectations(t)
}

// TestGetBatchHourlySpeciesData_LegacyDateParam pins the backwards-compatible form: the original
// single `date` param must still work and collapse to a one-day range.
func TestGetBatchHourlySpeciesData_LegacyDateParam(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("feature", "batch-hourly")

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2026-03-07", "2026-03-07",
		mock.Anything, 0.0).
		Return(map[string][24]int{sciBlackbird: {}}, nil)

	rec := callBatchHourly(t, mockDS, batchQuery([]string{sciBlackbird}, "date", "2026-03-07"))

	assert.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

// TestGetBatchHourlySpeciesData_InvalidRanges rejects malformed windows before touching the
// datastore. A half-specified range in particular must not be silently reinterpreted.
func TestGetBatchHourlySpeciesData_InvalidRanges(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "batch-hourly")

	tests := []struct {
		name  string
		query string
	}{
		{"start_date without end_date", batchQuery([]string{sciBlackbird}, "start_date", "2026-03-01")},
		{"end_date without start_date", batchQuery([]string{sciBlackbird}, "end_date", "2026-03-31")},
		{"end before start", batchQuery([]string{sciBlackbird}, "start_date", "2026-03-31", "end_date", "2026-03-01")},
		{"malformed start_date", batchQuery([]string{sciBlackbird}, "start_date", "03-01-2026", "end_date", "2026-03-31")},
		{"no date at all", batchQuery([]string{sciBlackbird})},
		{"no species", batchQuery(nil, "start_date", "2026-03-01", "end_date", "2026-03-31")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// No datastore expectations: a rejected request must not reach the database.
			mockDS := mocks.NewMockInterface(t)
			rec := callBatchHourly(t, mockDS, tt.query)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "expected 400 for %s", tt.name)
		})
	}
}

// TestGetBatchHourlySpeciesData_QueryFailureIsNotZeroes is a regression guard.
//
// Every requested species is pre-seeded with a zero-filled entry so a species with no detections
// renders as a flat zero line instead of vanishing. That seeding must not survive a failed query:
// if it does, the handler reports len(results) > 0, handleBatchResponse treats the request as a
// success, and a database error or query timeout is served as HTTP 200 with "no detections at any
// hour" - a silent wrong answer indistinguishable from a genuinely quiet period.
func TestGetBatchHourlySpeciesData_QueryFailureIsNotZeroes(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "regression")
	t.Attr("feature", "batch-hourly")

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2026-03-01", "2026-03-31",
		mock.Anything, 0.0).
		Return(map[string][24]int{}, errors.New("query timeout"))

	rec := callBatchHourly(t, mockDS, batchQuery([]string{sciBlackbird},
		"start_date", "2026-03-01", "end_date", "2026-03-31"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"a failed batch query must surface as an error, not as all-zero hourly counts")
	mockDS.AssertExpectations(t)
}

// TestGetBatchHourlySpeciesData_SpeciesWithNoRowsIsZeroFilled covers the other half of the same
// behaviour: on a SUCCESSFUL query, a requested species the datastore returned nothing for must
// still appear, zero-filled, so the chart draws it as a flat line instead of dropping the series.
func TestGetBatchHourlySpeciesData_SpeciesWithNoRowsIsZeroFilled(t *testing.T) {
	t.Parallel()
	t.Attr("component", "analytics")
	t.Attr("type", "integration")
	t.Attr("feature", "batch-hourly")

	mockDS := mocks.NewMockInterface(t)
	var hourly [24]int
	hourly[5] = 3

	// Only the blackbird comes back; the robin has no detections in the range.
	mockDS.On("GetBatchHourlyOccurrences", mock.Anything, "2026-03-01", "2026-03-31",
		mock.Anything, 0.0).
		Return(map[string][24]int{sciBlackbird: hourly}, nil)

	rec := callBatchHourly(t, mockDS, batchQuery([]string{sciBlackbird, sciRobin},
		"start_date", "2026-03-01", "end_date", "2026-03-31"))

	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string][]HourlyDistribution
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

	require.Contains(t, response, sciRobin, "a species with no detections must still be returned")
	require.Len(t, response[sciRobin], 24)
	total := 0
	for _, h := range response[sciRobin] {
		total += h.Count
	}
	assert.Equal(t, 0, total, "the absent species is zero-filled across all 24 hours")
	assert.Equal(t, 3, response[sciBlackbird][5].Count)
	mockDS.AssertExpectations(t)
}
