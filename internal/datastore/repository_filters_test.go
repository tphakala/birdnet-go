package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectionFilters_ToAdvancedSearchFilters(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Pacific/Auckland")
	require.NoError(t, err)
	verified := true
	locked := false

	tests := []struct {
		name    string
		filters *DetectionFilters
		want    AdvancedSearchFilters
		wantErr string
	}{
		{
			name:    "nil receiver uses the default limit",
			filters: nil,
			want:    AdvancedSearchFilters{Limit: DefaultDetectionLimit},
		},
		{
			name:    "empty filters keep zero values and no date range",
			filters: &DetectionFilters{},
			want:    AdvancedSearchFilters{},
		},
		{
			name: "every plain field is copied",
			filters: &DetectionFilters{
				Query:            "owl",
				Species:          []string{"Strix aluco"},
				TimeOfDay:        []string{"night"},
				Location:         []string{"node-1"},
				Limit:            25,
				Offset:           50,
				SortAscending:    true,
				Verified:         &verified,
				Locked:           &locked,
				MinID:            7,
				CursorPagination: true,
			},
			want: AdvancedSearchFilters{
				TextQuery:        "owl",
				Species:          []string{"Strix aluco"},
				TimeOfDay:        []string{"night"},
				Location:         []string{"node-1"},
				Limit:            25,
				Offset:           50,
				SortAscending:    true,
				Verified:         &verified,
				Locked:           &locked,
				MinID:            7,
				CursorPagination: true,
			},
		},
		{
			name:    "hour range and confidence are converted",
			filters: &DetectionFilters{HourRange: &HourRange{Start: 4, End: 6}, Confidence: &ConfidenceRange{Operator: ">=", Value: 0.8}},
			want: AdvancedSearchFilters{
				Hour:       &HourFilter{Start: 4, End: 6},
				Confidence: &ConfidenceFilter{Operator: ">=", Value: 0.8},
			},
		},
		{
			name:    "both dates are parsed in the zone",
			filters: &DetectionFilters{StartDate: "2026-04-05", EndDate: "2026-04-06"},
			want: AdvancedSearchFilters{DateRange: &DateRange{
				Start: time.Date(2026, 4, 5, 0, 0, 0, 0, loc),
				End:   time.Date(2026, 4, 6, 0, 0, 0, 0, loc),
			}},
		},
		{
			name:    "only an end date leaves the start zero",
			filters: &DetectionFilters{EndDate: "2026-04-06"},
			want: AdvancedSearchFilters{DateRange: &DateRange{
				End: time.Date(2026, 4, 6, 0, 0, 0, 0, loc),
			}},
		},
		{
			name:    "an invalid start date is an error",
			filters: &DetectionFilters{StartDate: "05/04/2026"},
			wantErr: `invalid date "05/04/2026"`,
		},
		{
			name:    "an invalid end date is an error",
			filters: &DetectionFilters{StartDate: "2026-04-05", EndDate: "tomorrow"},
			wantErr: `invalid date "tomorrow"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.filters.ToAdvancedSearchFilters(loc)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, AdvancedSearchFilters{}, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectionFilters_ToAdvancedSearchFilters_OnlyStartDateEndsNow(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)

	before := time.Now()
	got, err := (&DetectionFilters{StartDate: "2026-01-02"}).ToAdvancedSearchFilters(loc)
	after := time.Now()
	require.NoError(t, err)
	require.NotNil(t, got.DateRange)

	assert.Equal(t, time.Date(2026, 1, 2, 0, 0, 0, 0, loc), got.DateRange.Start)
	assert.Equal(t, loc, got.DateRange.End.Location())
	assert.False(t, got.DateRange.End.Before(before))
	assert.False(t, got.DateRange.End.After(after))
}

func TestDateRangeSearchFilters(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	t.Run("both bounds are parsed in the zone with paging", func(t *testing.T) {
		t.Parallel()
		got, err := DateRangeSearchFilters("2026-11-01", "2026-11-02", 10, 20, loc)
		require.NoError(t, err)
		assert.Equal(t, AdvancedSearchFilters{
			DateRange: &DateRange{
				Start: time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
				End:   time.Date(2026, 11, 2, 0, 0, 0, 0, loc),
			},
			Limit:  10,
			Offset: 20,
		}, got)
	})

	t.Run("empty bounds give a zero start and an end of now", func(t *testing.T) {
		t.Parallel()
		before := time.Now()
		got, err := DateRangeSearchFilters("", "", 0, 0, loc)
		after := time.Now()
		require.NoError(t, err)
		require.NotNil(t, got.DateRange)
		assert.True(t, got.DateRange.Start.IsZero())
		assert.Equal(t, loc, got.DateRange.End.Location())
		assert.False(t, got.DateRange.End.Before(before))
		assert.False(t, got.DateRange.End.After(after))
	})

	t.Run("an invalid start is an error", func(t *testing.T) {
		t.Parallel()
		_, err := DateRangeSearchFilters("x", "", 1, 0, loc)
		require.ErrorContains(t, err, `invalid date "x"`)
	})

	t.Run("an invalid end is an error", func(t *testing.T) {
		t.Parallel()
		_, err := DateRangeSearchFilters("2026-11-01", "y", 1, 0, loc)
		require.ErrorContains(t, err, `invalid date "y"`)
	})
}
