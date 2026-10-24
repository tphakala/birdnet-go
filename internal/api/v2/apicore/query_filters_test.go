package apicore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestParseDateRangeFilter_OneSidedRangeIsOpenEnded pins that setting only a
// start or only an end date still filters, instead of being dropped (which made
// the list show every date while the filter panel claimed a date was applied).
func TestParseDateRangeFilter_OneSidedRangeIsOpenEnded(t *testing.T) {
	t.Parallel()

	const day = "2024-03-10"
	dayStart, err := time.Parse(time.DateOnly, day)
	require.NoError(t, err)

	t.Run("start only runs through today", func(t *testing.T) {
		t.Parallel()
		got := ParseDateRangeFilter("", day, "")
		require.NotNil(t, got)
		assert.True(t, got.Start.Equal(dayStart))
		assert.False(t, got.End.Before(time.Now().Add(-24*time.Hour)), "end must be today")
	})

	t.Run("end only starts at the beginning", func(t *testing.T) {
		t.Parallel()
		got := ParseDateRangeFilter("", "", day)
		require.NotNil(t, got)
		assert.Equal(t, openRangeStartYear, got.Start.Year())
		assert.True(t, got.End.Equal(dayStart.AddDate(0, 0, 1).Add(-time.Second)))
	})

	t.Run("start in the future keeps the filter and matches nothing", func(t *testing.T) {
		t.Parallel()
		got := ParseDateRangeFilter("", "2999-01-01", "")
		require.NotNil(t, got, "dropping the range would return every detection")
		assert.Equal(t, 2999, got.Start.Year())
		assert.Equal(t, 2999, got.End.Year())
	})

	t.Run("end before the open start keeps the filter and matches nothing", func(t *testing.T) {
		t.Parallel()
		got := ParseDateRangeFilter("", "", "1960-05-01")
		require.NotNil(t, got)
		assert.Equal(t, 1960, got.Start.Year())
		assert.Equal(t, 1960, got.End.Year())
	})

	t.Run("two explicit dates in the wrong order are rejected", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ParseDateRangeFilter("", "2024-06-10", "2024-06-01"))
	})

	t.Run("unparseable bound is rejected", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ParseDateRangeFilter("", "nope", ""))
		assert.Nil(t, ParseDateRangeFilter("", "", "nope"))
	})
}
