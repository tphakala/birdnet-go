// analytics_range_params_test.go: the start_date/end_date handling shared by the
// range analytics endpoints, tested once over every endpoint instead of once per
// endpoint.
package analytics

import (
	"context"
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// Dates shared by the range-parameter tests. defaultRangeEnd is rangeStart plus the
// 30 day window the handlers apply when end_date is omitted.
const (
	rangeStart      = "2026-03-01"
	rangeEnd        = "2026-03-02"
	defaultRangeEnd = "2026-03-31"
	reversedStart   = "2026-03-05"
	// impossibleDate is well formed but not a calendar date.
	impossibleDate = "2026-02-30"
)

// rangeEndpoint describes one endpoint that requires start_date and defaults
// end_date to a 30 day window. expect registers the datastore call the handler makes
// for [start, end] and returns sample data, or err when err is not nil.
type rangeEndpoint struct {
	name    string
	path    string
	handler func(*Handler, echo.Context) error
	expect  func(m *mocks.MockInterface, start, end string, err error)
}

func rangeEndpoints() []rangeEndpoint {
	return []rangeEndpoint{
		{
			name:    "species hourly distribution",
			path:    "/api/v2/analytics/time/distribution/species",
			handler: (*Handler).GetSpeciesHourlyDistribution,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetHourlyDistributionBySpecies", mock.Anything, start, end, []string(nil), defaultSpeciesRidgelineLimit).
					Return(sampleSpeciesDistribution(), err)
			},
		},
		{
			name:    "acoustic succession",
			path:    "/api/v2/analytics/time/succession",
			handler: (*Handler).GetAcousticSuccession,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetAcousticSuccession", mock.Anything, start, end, []string(nil), defaultSpeciesSuccessionLimit).
					Return(sampleAcousticSuccession(), err)
			},
		},
		{
			name:    "species accumulation",
			path:    "/api/v2/analytics/species/accumulation",
			handler: (*Handler).GetSpeciesAccumulation,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetSpeciesAccumulation", mock.Anything, start, end).
					Return(sampleSpeciesAccumulation(), err)
			},
		},
		{
			name:    "confidence distribution",
			path:    "/api/v2/analytics/confidence/distribution",
			handler: (*Handler).GetConfidenceDistribution,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetConfidenceHistogram", mock.Anything, start, end, "", defaultConfidenceBins, defaultSpeciesRidgelineLimit).
					Return(sampleConfidenceDistribution(), err)
			},
		},
		{
			name:    "species phenology",
			path:    "/api/v2/analytics/species/phenology",
			handler: (*Handler).GetSpeciesPhenology,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetSpeciesPhenology", mock.Anything, start, end, defaultSpeciesPhenologyLimit).
					Return(sampleSpeciesPhenology(), err)
			},
		},
		{
			name:    "activity heatmap",
			path:    "/api/v2/analytics/time/heatmap",
			handler: (*Handler).GetActivityHeatmap,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetActivityHeatmap", mock.Anything, start, end, "").
					Return(sampleHeatmap(), err)
			},
		},
		{
			name:    "dawn chorus onset",
			path:    "/api/v2/analytics/time/dawn-onset",
			handler: (*Handler).GetDawnChorusOnset,
			expect: func(m *mocks.MockInterface, start, end string, err error) {
				m.On("GetDailyActivityOnset", mock.Anything, start, end, "").
					Return(sampleDawnOnset(), err)
			},
		},
	}
}

// TestRangeEndpoints_RejectBadDates checks that every range endpoint answers a
// missing start_date, an unparseable one and a reversed range with 400 before any
// datastore call (the mock has no expectation, so a call would fail the test).
func TestRangeEndpoints_RejectBadDates(t *testing.T) {
	t.Parallel()

	queries := []struct {
		name  string
		query string
	}{
		{name: "missing start_date", query: ""},
		{name: "invalid start_date format", query: "?start_date=not-a-date"},
		{name: "impossible start_date", query: "?start_date=" + impossibleDate},
		{name: "impossible end_date", query: "?start_date=" + rangeStart + "&end_date=" + impossibleDate},
		{name: "reversed range", query: "?start_date=" + reversedStart + "&end_date=" + rangeStart},
	}

	for _, ep := range rangeEndpoints() {
		for _, q := range queries {
			t.Run(ep.name+"/"+q.name, func(t *testing.T) {
				t.Parallel()
				e, _, controller := setupAnalyticsTestEnvironment(t)

				c, rec := newAnalyticsContext(e, ep.path+q.query)
				// The handlers write the 400 response themselves and also return the error.
				require.Error(t, ep.handler(controller, c))
				assert.Equal(t, http.StatusBadRequest, rec.Code)
			})
		}
	}
}

// TestRangeEndpoints_DefaultEndDate checks that omitting end_date queries a 30 day
// window from start_date on every range endpoint.
func TestRangeEndpoints_DefaultEndDate(t *testing.T) {
	t.Parallel()

	for _, ep := range rangeEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)
			ep.expect(mockDS, rangeStart, defaultRangeEnd, nil)

			c, rec := newAnalyticsContext(e, ep.path+"?start_date="+rangeStart)
			require.NoError(t, ep.handler(controller, c))
			require.Equal(t, http.StatusOK, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}

// TestRangeEndpoints_QueryTimeout checks that a datastore deadline error becomes a
// 408 response on every range endpoint. handleAnalyticsQueryError writes the response
// and returns nil, so the status is asserted rather than the returned error.
func TestRangeEndpoints_QueryTimeout(t *testing.T) {
	t.Parallel()

	for _, ep := range rangeEndpoints() {
		t.Run(ep.name, func(t *testing.T) {
			t.Parallel()
			e, mockDS, controller := setupAnalyticsTestEnvironment(t)
			ep.expect(mockDS, rangeStart, rangeEnd, context.DeadlineExceeded)

			c, rec := newAnalyticsContext(e, ep.path+"?start_date="+rangeStart+"&end_date="+rangeEnd)
			require.NoError(t, ep.handler(controller, c))
			assert.Equal(t, http.StatusRequestTimeout, rec.Code)
			mockDS.AssertExpectations(t)
		})
	}
}
