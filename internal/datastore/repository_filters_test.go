package datastore

import (
	"context"
	"fmt"
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
			name:    "the start date error is reported before the end date error",
			filters: &DetectionFilters{StartDate: "bad-start", EndDate: "bad-end"},
			wantErr: `invalid date "bad-start"`,
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

	t.Run("the start error is reported before the end error", func(t *testing.T) {
		t.Parallel()
		_, err := DateRangeSearchFilters("bad-start", "bad-end", 1, 0, loc)
		require.ErrorContains(t, err, `invalid date "bad-start"`)
	})

	t.Run("an invalid end is an error", func(t *testing.T) {
		t.Parallel()
		_, err := DateRangeSearchFilters("2026-11-01", "y", 1, 0, loc)
		require.ErrorContains(t, err, `invalid date "y"`)
	})
}

// searchCapturingStore records the filters the bridge passes to SearchNotesAdvanced.
// The embedded Interface is nil: the bridge methods under test call nothing else.
type searchCapturingStore struct {
	Interface
	got *AdvancedSearchFilters
}

func (s *searchCapturingStore) SearchNotesAdvanced(filters *AdvancedSearchFilters) ([]Note, int64, error) {
	s.got = filters
	return nil, 0, nil
}

func TestDetectionRepository_DateFiltersUseRepositoryZone(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Pacific/Auckland")
	require.NoError(t, err)
	wantStart := time.Date(2026, 4, 5, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 4, 6, 0, 0, 0, 0, loc)

	tests := []struct {
		name string
		call func(ctx context.Context, repo DetectionRepository) error
	}{
		{
			name: "Search",
			call: func(ctx context.Context, repo DetectionRepository) error {
				_, _, err := repo.Search(ctx, &DetectionFilters{StartDate: "2026-04-05", EndDate: "2026-04-06"})
				return err
			},
		},
		{
			name: "GetByDateRange",
			call: func(ctx context.Context, repo DetectionRepository) error {
				_, _, err := repo.GetByDateRange(ctx, "2026-04-05", "2026-04-06", 10, 0)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := &searchCapturingStore{}
			require.NoError(t, tt.call(t.Context(), NewDetectionRepository(store, loc)))

			require.NotNil(t, store.got)
			require.NotNil(t, store.got.DateRange)
			assert.True(t, wantStart.Equal(store.got.DateRange.Start), "start %v", store.got.DateRange.Start)
			assert.True(t, wantEnd.Equal(store.got.DateRange.End), "end %v", store.got.DateRange.End)
		})
	}
}

// TestDetectionRepository_SearchAdvancedDefaultSortMatchesSearchNotes verifies on a real
// legacy SQLite store that SearchAdvanced with SortBySearchDefault returns the same IDs,
// in the same order, with the same total as SearchNotes, at the first and a second page.
// Every seeded row has a name: for an empty query SearchNotes adds LIKE '%%', which drops
// rows whose scientific and common names are both NULL, and SearchAdvanced adds no
// predicate, so the two differ on such rows.
func TestDetectionRepository_SearchAdvancedDefaultSortMatchesSearchNotes(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteLock{}, &NoteComment{}))
	ds := &SQLiteStore{DB: db}
	repo := NewDetectionRepository(ds, time.UTC)

	// Later rows are older, so an ID-ordered result differs from a time-ordered one.
	seeds := []struct{ scientific, common string }{
		{"Corvus corone", "Carrion Crow"},
		{"Erithacus rubecula", "European Robin"},
		{"Corvus frugilegus", "Rook"},
		{"Corvus corone", "Carrion Crow"},
		{"Erithacus rubecula", "European Robin"},
		{"Corvus corone", "Carrion Crow"},
	}
	for i, s := range seeds {
		require.NoError(t, db.Create(&Note{
			Date:           "2026-06-01",
			Time:           fmt.Sprintf("%02d:00:00", len(seeds)-i),
			ScientificName: s.scientific,
			CommonName:     s.common,
			Confidence:     0.9,
		}).Error)
	}

	const pageSize = 2
	queries := []struct {
		name  string
		query string
		want  int64
	}{
		{"empty query", "", 6},
		{"scientific substring", "Corvus", 4},
		{"common name substring", "Robin", 2},
		{"no match", "Pinson", 0},
	}
	for _, q := range queries {
		for _, offset := range []int{0, pageSize} {
			t.Run(fmt.Sprintf("%s offset %d", q.name, offset), func(t *testing.T) {
				notes, noteTotal, err := ds.SearchNotes(q.query, false, pageSize, offset)
				require.NoError(t, err)
				require.Equal(t, q.want, noteTotal, "SearchNotes total")

				results, total, err := repo.SearchAdvanced(t.Context(), &AdvancedSearchFilters{
					TextQuery: q.query,
					SortBy:    SortBySearchDefault,
					Limit:     pageSize,
					Offset:    offset,
				})
				require.NoError(t, err)

				assert.Equal(t, noteTotal, total, "total")
				wantIDs := make([]uint, len(notes))
				for i := range notes {
					wantIDs[i] = notes[i].ID
				}
				gotIDs := make([]uint, len(results))
				for i := range results {
					gotIDs[i] = results[i].ID
				}
				assert.Equal(t, wantIDs, gotIDs, "IDs in order")
			})
		}
	}
}
