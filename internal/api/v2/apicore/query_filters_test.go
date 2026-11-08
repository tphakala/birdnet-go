package apicore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// confidenceValueDelta is the tolerance used when comparing parsed confidence
// fractions, since values like 0.1 have no exact binary float representation.
const confidenceValueDelta = 1e-9

// TestParseConfidenceFilter pins the operator/value parsing of the confidence
// filter, including the previously-broken explicit "=N" form. Before the fix a
// bare "50" parsed as equality but the documented "=50" fell through to the
// default case with value "=50", which strconv.ParseFloat rejected, silently
// dropping the whole filter. Both forms must now parse to operator "=".
func TestParseConfidenceFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		param        string
		wantNil      bool
		wantOperator string
		wantValue    float64 // fraction, i.e. param/100
	}{
		{
			name:         "explicit equals operator",
			param:        "=50",
			wantOperator: "=",
			wantValue:    0.5,
		},
		{
			name:         "bare number defaults to equals",
			param:        "50",
			wantOperator: "=",
			wantValue:    0.5,
		},
		{
			name:         "greater than or equal",
			param:        ">=80",
			wantOperator: ">=",
			wantValue:    0.8,
		},
		{
			name:         "less than or equal",
			param:        "<=20",
			wantOperator: "<=",
			wantValue:    0.2,
		},
		{
			name:         "greater than",
			param:        ">10",
			wantOperator: ">",
			wantValue:    0.1,
		},
		{
			name:         "less than",
			param:        "<90",
			wantOperator: "<",
			wantValue:    0.9,
		},
		{
			name:         "explicit equals zero boundary",
			param:        "=0",
			wantOperator: "=",
			wantValue:    0.0,
		},
		{
			name:         "explicit equals hundred boundary",
			param:        "=100",
			wantOperator: "=",
			wantValue:    1.0,
		},
		{
			name:    "empty string returns nil",
			param:   "",
			wantNil: true,
		},
		{
			name:    "explicit equals with non-numeric value returns nil",
			param:   "=abc",
			wantNil: true,
		},
		{
			name:    "lone equals returns nil",
			param:   "=",
			wantNil: true,
		},
		{
			name:    "explicit equals out of range high returns nil",
			param:   "=150",
			wantNil: true,
		},
		{
			name:    "explicit equals negative returns nil",
			param:   "=-5",
			wantNil: true,
		},
		{
			name:    "explicit equals NaN returns nil",
			param:   "=NaN",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ParseConfidenceFilter(tt.param)

			if tt.wantNil {
				assert.Nil(t, got, "expected nil result for param %q", tt.param)
				return
			}

			require.NotNil(t, got, "expected non-nil result for param %q", tt.param)
			assert.Equal(t, tt.wantOperator, got.Operator, "operator mismatch for param %q", tt.param)
			assert.InDelta(t, tt.wantValue, got.Value, confidenceValueDelta, "value mismatch for param %q", tt.param)
		})
	}
}

// TestParseDateRangeFilter pins that date shortcuts resolve on the station's wall-clock calendar
// and that week and month are inclusive ranges of local dates ending today. The expected dates
// are what repository.DateRangeToUnix reads from the carriers (their year, month and day).
func TestParseDateRangeFilter(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)

	tests := []struct {
		name        string
		zone        string
		now         string // wall-clock time "2006-01-02 15:04" read in nowZone
		nowZone     string // zone of the instant; defaults to zone
		nilLoc      bool   // pass a nil loc, and read now in time.Local
		single      string
		start, end  string
		wantNil     bool
		first, last string
	}{
		{
			name: "today on a skipped-midnight day is that date", zone: "America/Havana",
			now:    "2026-03-08 01:00",
			single: "today", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "yesterday landing on a skipped-midnight date", zone: "America/Havana",
			now:    "2026-03-09 12:00",
			single: "yesterday", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "week is the last 7 dates including today", zone: "America/Havana",
			now:    "2026-03-14 12:00",
			single: "week", first: "2026-03-08", last: "2026-03-14",
		},
		{
			name: "month is the last 30 dates including today", zone: "America/Havana",
			now:    "2026-04-06 12:00",
			single: "month", first: "2026-03-08", last: "2026-04-06",
		},
		{
			name: "today in the southern-hemisphere skipped midnight", zone: "America/Santiago",
			now:    "2026-09-06 01:00",
			single: "today", first: "2026-09-06", last: "2026-09-06",
		},
		{
			name: "yesterday the day after the southern skipped midnight", zone: "America/Santiago",
			now:    "2026-09-07 12:00",
			single: "yesterday", first: "2026-09-06", last: "2026-09-06",
		},
		{
			name: "yesterday counts dates not elapsed hours the day after spring forward", zone: "America/New_York",
			now:    "2026-03-09 00:30",
			single: "yesterday", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "today is read in the station zone when now is given in UTC", zone: "Europe/Helsinki",
			now: "2026-07-08 22:30", nowZone: "UTC",
			single: "today", first: "2026-07-09", last: "2026-07-09",
		},
		{
			name: "week across a year boundary", zone: "UTC",
			now:    "2026-01-03 12:00",
			single: "week", first: "2025-12-28", last: "2026-01-03",
		},
		{
			name: "month across a leap day", zone: "UTC",
			now:    "2028-03-05 12:00",
			single: "month", first: "2028-02-05", last: "2028-03-05",
		},
		{
			name: "shortcut matching is case-insensitive", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "TODAY", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "an explicit single date is a one-date range", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "2026-01-15", first: "2026-01-15", last: "2026-01-15",
		},
		{
			name: "an unknown token falls through to start and end", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "fortnight", start: "2026-03-01", end: "2026-03-08", first: "2026-03-01", last: "2026-03-08",
		},
		{
			name: "a parseable single date wins over start and end", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "2026-01-15", start: "2026-03-01", end: "2026-03-08", first: "2026-01-15", last: "2026-01-15",
		},
		{
			name: "a shortcut wins over start and end", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "yesterday", start: "2026-03-01", end: "2026-03-02", first: "2026-03-07", last: "2026-03-07",
		},
		{
			// A nil loc must read the date in time.Local, not UTC. Near local midnight a UTC
			// reading lands on another date on any host east of UTC (00:30) or west of it (23:30).
			name: "a nil loc reads the date in time.Local just after midnight", zone: "", nilLoc: true,
			now:    "2026-03-08 00:30",
			single: "today", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "a nil loc reads the date in time.Local just before midnight", zone: "", nilLoc: true,
			now:    "2026-03-08 23:30",
			single: "today", first: "2026-03-08", last: "2026-03-08",
		},
		{
			name: "an unknown token alone yields no range", zone: "UTC",
			now:    "2026-03-08 12:00",
			single: "fortnight", wantNil: true,
		},
		{
			name: "an inverted start and end is rejected", zone: "UTC",
			now:   "2026-03-08 12:00",
			start: "2026-03-10", end: "2026-03-01", wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var loc, nowLoc *time.Location
			if tt.nilLoc {
				nowLoc = time.Local
			} else {
				loc = zones[tt.zone]
				require.NotNil(t, loc, tt.zone)
				nowZone := tt.nowZone
				if nowZone == "" {
					nowZone = tt.zone
				}
				nowLoc = zones[nowZone]
				require.NotNil(t, nowLoc, nowZone)
			}
			now, err := time.ParseInLocation("2006-01-02 15:04", tt.now, nowLoc)
			require.NoError(t, err)
			r := ParseDateRangeFilter(tt.single, tt.start, tt.end, now, loc)
			if tt.wantNil {
				assert.Nil(t, r)
				return
			}
			require.NotNil(t, r)
			assert.Equal(t, tt.first, wallclock.DateOf(r.Start).String(), "first date")
			assert.Equal(t, tt.last, wallclock.DateOf(r.End).String(), "last date")
		})
	}
}

// TestIsDateShortcut pins that request validation accepts exactly the tokens the resolver does.
func TestIsDateShortcut(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"today", "yesterday", "week", "month", "TODAY", "WeEk"} {
		assert.True(t, IsDateShortcut(token), token)
	}
	for _, token := range []string{"", "2026-03-08", "fortnight", " today", "today "} {
		assert.False(t, IsDateShortcut(token), token)
	}
}
