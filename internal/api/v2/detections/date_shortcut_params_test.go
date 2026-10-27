package detections

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// shortcutWindow returns the first and last date of the range a shortcut resolves to, read from the
// current local date of a shortcut covering days dates.
func shortcutWindow(days int) (first, last wallclock.Date) {
	today := wallclock.DateOf(time.Now().In(time.Local))
	return today.AddDays(1 - days), today
}

// newShortcutHandler builds a handler for a parallel test.
func newShortcutHandler(t *testing.T) *Handler {
	t.Helper()
	_, _, h := setupTestEnvironment(t, apitest.WithoutSettingsPublish())
	return h
}

// TestParseDetectionQueryParams_DateShortcuts pins that the date query parameter accepts the
// search box shortcuts (case-insensitively) and that the advanced search filters carry the
// resolved local dates; anything else that is not YYYY-MM-DD is still rejected.
func TestParseDetectionQueryParams_DateShortcuts(t *testing.T) {
	t.Parallel()

	const (
		weekDays  = 7
		monthDays = 30
	)

	tests := []struct {
		name     string
		query    url.Values
		days     int // number of dates the range covers, ending today or yesterday
		endShift int // days before today the range ends
	}{
		{name: "today", query: url.Values{"date": {"today"}}, days: 1},
		{name: "yesterday", query: url.Values{"date": {"yesterday"}}, days: 1, endShift: 1},
		{name: "week", query: url.Values{"date": {"week"}}, days: weekDays},
		{name: "month", query: url.Values{"date": {"month"}}, days: monthDays},
		{name: "mixed case", query: url.Values{"date": {"WeEk"}}, days: weekDays},
		{name: "with a search term", query: url.Values{"search": {"robin"}, "date": {"month"}}, days: monthDays},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, c := setupTestEnvironment(t, apitest.WithoutSettingsPublish())
			req := httptest.NewRequest("GET", "/api/v2/detections?"+tt.query.Encode(), http.NoBody)
			ctx := echo.New().NewContext(req, httptest.NewRecorder())

			params, err := c.parseDetectionQueryParams(ctx)
			require.NoError(t, err)
			require.NotNil(t, params)

			beforeFirst, beforeLast := shortcutWindow(tt.days)
			filters := c.buildAdvancedSearchFilters(params)
			afterFirst, afterLast := shortcutWindow(tt.days)
			require.NotNil(t, filters.DateRange)

			gotFirst := wallclock.DateOf(filters.DateRange.Start)
			gotLast := wallclock.DateOf(filters.DateRange.End)
			wantFirst := []wallclock.Date{beforeFirst.AddDays(-tt.endShift), afterFirst.AddDays(-tt.endShift)}
			wantLast := []wallclock.Date{beforeLast.AddDays(-tt.endShift), afterLast.AddDays(-tt.endShift)}
			assert.Contains(t, wantFirst, gotFirst, "first date")
			assert.Contains(t, wantLast, gotLast, "last date")
		})
	}

	t.Run("an explicit date is a one-date range", func(t *testing.T) {
		t.Parallel()
		_, _, c := setupTestEnvironment(t, apitest.WithoutSettingsPublish())
		req := httptest.NewRequest("GET", "/api/v2/detections?date=2026-01-15", http.NoBody)
		ctx := echo.New().NewContext(req, httptest.NewRecorder())
		params, err := c.parseDetectionQueryParams(ctx)
		require.NoError(t, err)
		filters := c.buildAdvancedSearchFilters(params)
		require.NotNil(t, filters.DateRange)
		assert.Equal(t, "2026-01-15", wallclock.DateOf(filters.DateRange.Start).String())
		assert.Equal(t, "2026-01-15", wallclock.DateOf(filters.DateRange.End).String())
	})

	for _, bad := range []string{"fortnight", "today ", "2026-1-5", "week;drop"} {
		t.Run("still rejects "+bad, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/api/v2/detections?"+url.Values{"date": {bad}}.Encode(), http.NoBody)
			ctx := echo.New().NewContext(req, httptest.NewRecorder())
			_, err := newShortcutHandler(t).parseDetectionQueryParams(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid date format, use YYYY-MM-DD")
		})
	}

	t.Run("hourly and species queries keep rejecting shortcuts", func(t *testing.T) {
		t.Parallel()
		for _, qt := range []string{"hourly", "species"} {
			req := httptest.NewRequest("GET", "/api/v2/detections?queryType="+qt+"&date=week", http.NoBody)
			ctx := echo.New().NewContext(req, httptest.NewRecorder())
			_, err := newShortcutHandler(t).parseDetectionQueryParams(ctx)
			require.Error(t, err, qt)
		}
	})

	t.Run("start_date and end_date reject shortcuts", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest("GET", "/api/v2/detections?start_date=today&end_date=today", http.NoBody)
		ctx := echo.New().NewContext(req, httptest.NewRecorder())
		_, err := newShortcutHandler(t).parseDetectionQueryParams(ctx)
		require.Error(t, err)
	})
}
