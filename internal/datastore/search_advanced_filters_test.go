package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAdvancedSearchTestDB returns an in-memory legacy-schema store seeded with a
// fixed set of notes used across the advanced-filter tests below.
//
// The seed deliberately spreads detections across confidence bands, review
// verdicts and times of day so a single fixture can exercise every filter
// without each test re-describing the whole dataset.
func newAdvancedSearchTestDB(t *testing.T) *DataStore {
	t.Helper()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}, &NoteLock{}, &NoteComment{}))

	notes := []Note{
		// id 1: low confidence, reviewed correct, mid-morning
		{ID: 1, Date: "2026-05-10", Time: "09:30:00", ScientificName: "Parus major", CommonName: "Great Tit", Confidence: 0.35},
		// id 2: mid confidence, reviewed false positive, midday
		{ID: 2, Date: "2026-05-10", Time: "12:00:00", ScientificName: "Corvus corax", CommonName: "Common Raven", Confidence: 0.65},
		// id 3: high confidence, never reviewed, midday
		{ID: 3, Date: "2026-05-10", Time: "13:15:00", ScientificName: "Turdus merula", CommonName: "Blackbird", Confidence: 0.95},
		// id 4: mid confidence, review row with an empty verdict (reviewed but undecided)
		{ID: 4, Date: "2026-05-10", Time: "22:45:00", ScientificName: "Strix aluco", CommonName: "Tawny Owl", Confidence: 0.70},
		// id 5: high confidence, never reviewed, inside the fallback sunrise window
		{ID: 5, Date: "2026-05-10", Time: "05:30:00", ScientificName: "Erithacus rubecula", CommonName: "Robin", Confidence: 0.88},
	}
	for i := range notes {
		require.NoError(t, db.Create(&notes[i]).Error)
	}

	reviews := []NoteReview{
		{NoteID: 1, Verified: VerifiedStatusCorrect},
		{NoteID: 2, Verified: VerifiedStatusFalsePositive},
		{NoteID: 4, Verified: ""},
	}
	for i := range reviews {
		require.NoError(t, db.Create(&reviews[i]).Error)
	}

	return &DataStore{DB: db}
}

// resultIDs extracts the note IDs from a search result, for order-insensitive
// comparison against the expected set.
func resultIDs(notes []Note) []uint {
	ids := make([]uint, 0, len(notes))
	for i := range notes {
		ids = append(ids, notes[i].ID)
	}
	return ids
}

func TestNormalizeTimeOfDayPeriods(t *testing.T) {
	t.Parallel()

	t.Run("empty input yields no constraint", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, NormalizeTimeOfDayPeriods(nil))
		assert.Nil(t, NormalizeTimeOfDayPeriods([]string{}))
	})

	t.Run("canonical periods pass through", func(t *testing.T) {
		t.Parallel()
		in := []string{TimeOfDayDay, TimeOfDayNight, TimeOfDaySunrise, TimeOfDaySunset}
		assert.Equal(t, in, NormalizeTimeOfDayPeriods(in))
	})

	t.Run("legacy dawn and dusk map onto sunrise and sunset", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{TimeOfDaySunrise}, NormalizeTimeOfDayPeriods([]string{"dawn"}))
		assert.Equal(t, []string{TimeOfDaySunset}, NormalizeTimeOfDayPeriods([]string{"dusk"}))
	})

	t.Run("an alias and its canonical name collapse to one period", func(t *testing.T) {
		t.Parallel()
		// Without deduplication the same period would be OR-ed into the query twice.
		assert.Equal(t, []string{TimeOfDaySunrise},
			NormalizeTimeOfDayPeriods([]string{"dawn", TimeOfDaySunrise, "DAWN"}))
	})

	t.Run("case and surrounding space are ignored", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{TimeOfDayNight}, NormalizeTimeOfDayPeriods([]string{"  NiGhT "}))
	})

	t.Run("any and unrecognized values are dropped", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, NormalizeTimeOfDayPeriods([]string{TimeOfDayAny}))
		assert.Nil(t, NormalizeTimeOfDayPeriods([]string{""}))
		assert.Nil(t, NormalizeTimeOfDayPeriods([]string{"evening", "twilight"}))
	})

	t.Run("recognized values survive alongside unrecognized ones", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []string{TimeOfDayDay},
			NormalizeTimeOfDayPeriods([]string{"evening", TimeOfDayDay, TimeOfDayAny}))
	})
}

// TestSearchNotesAdvanced_LegacyVerifiedBool guards the pre-existing two-state
// behaviour, which stays available for callers that have not migrated.
func TestSearchNotesAdvanced_LegacyVerifiedBool(t *testing.T) {
	t.Parallel()

	t.Run("true selects rows carrying any verdict", func(t *testing.T) {
		t.Parallel()
		ds := newAdvancedSearchTestDB(t)
		reviewed := true

		notes, _, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{Verified: &reviewed})
		require.NoError(t, err)

		assert.ElementsMatch(t, []uint{1, 2}, resultIDs(notes))
	})

	t.Run("false selects rows carrying none", func(t *testing.T) {
		t.Parallel()
		ds := newAdvancedSearchTestDB(t)
		reviewed := false

		notes, _, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{Verified: &reviewed})
		require.NoError(t, err)

		assert.ElementsMatch(t, []uint{3, 4, 5}, resultIDs(notes))
	})
}

// TestSearchNotesAdvanced_TimeOfDayWithoutSunCalc covers the fallback used when
// no station location is configured, so SunCalc cannot resolve real sun events.
// The filter must still narrow the result set rather than being dropped.
func TestSearchNotesAdvanced_TimeOfDayWithoutSunCalc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		periods []string
		wantIDs []uint
	}{
		{
			// Fallback day window is 07:00-18:00: ids 1 (09:30), 2 (12:00), 3 (13:15).
			name:    "day",
			periods: []string{TimeOfDayDay},
			wantIDs: []uint{1, 2, 3},
		},
		{
			// Fallback night window is 20:00-05:00: id 4 (22:45).
			name:    "night",
			periods: []string{TimeOfDayNight},
			wantIDs: []uint{4},
		},
		{
			// Fallback sunrise window is 05:00-07:00: id 5 (05:30).
			name:    "sunrise",
			periods: []string{TimeOfDaySunrise},
			wantIDs: []uint{5},
		},
		{
			name:    "legacy dawn alias matches the sunrise window",
			periods: []string{"dawn"},
			wantIDs: []uint{5},
		},
		{
			name:    "multiple periods are unioned",
			periods: []string{TimeOfDaySunrise, TimeOfDayNight},
			wantIDs: []uint{4, 5},
		},
		{
			name:    "any is not a constraint",
			periods: []string{TimeOfDayAny},
			wantIDs: []uint{1, 2, 3, 4, 5},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ds := newAdvancedSearchTestDB(t)
			require.Nil(t, ds.SunCalc, "this fixture has no station location")

			notes, _, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{TimeOfDay: tc.periods})
			require.NoError(t, err)

			assert.ElementsMatch(t, tc.wantIDs, resultIDs(notes))
		})
	}
}

// TestApplyTimeOfDayFilterUnknownPeriodIsNoConstraint pins the behaviour of an
// unrecognized period at the query-building level: it must leave the query
// untouched rather than emit a clause that matches nothing.
func TestApplyTimeOfDayFilterUnknownPeriodIsNoConstraint(t *testing.T) {
	t.Parallel()

	ds := newAdvancedSearchTestDB(t)

	notes, _, err := ds.SearchNotesAdvanced(&AdvancedSearchFilters{
		TimeOfDay: []string{"midafternoon"},
	})
	require.NoError(t, err)

	assert.Len(t, notes, 5, "an unrecognized period must not silently hide rows")
}

// TestSearchNotesAdvanced_RejectsFiltersOnlyTheNormalizedStoreImplements pins
// that the deprecated legacy store refuses the confidence band and the review
// verdict instead of ignoring them, which would return rows the caller asked to
// exclude.
func TestSearchNotesAdvanced_RejectsFiltersOnlyTheNormalizedStoreImplements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		filters AdvancedSearchFilters
		wantErr bool
	}{
		{
			name:    "confidence band",
			filters: AdvancedSearchFilters{ConfidenceRange: &ConfidenceRangeFilter{Min: 0.5, Max: ConfidenceUpperBound}},
			wantErr: true,
		},
		{
			name:    "review verdict",
			filters: AdvancedSearchFilters{VerifiedStatus: VerifiedStatusFalsePositive},
			wantErr: true,
		},
		{
			name:    "verdict any is no constraint",
			filters: AdvancedSearchFilters{VerifiedStatus: VerifiedStatusAny},
		},
		{
			name:    "no new filters",
			filters: AdvancedSearchFilters{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ds := newAdvancedSearchTestDB(t)

			notes, _, err := ds.SearchNotesAdvanced(&tc.filters)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "legacy datastore")
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, notes)
		})
	}
}
