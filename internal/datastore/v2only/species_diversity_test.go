package v2only

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// diversitySeed is one detection seeded for the species diversity tests.
type diversitySeed struct {
	species       string
	at            time.Time
	falsePositive bool
}

// expectedDiversity computes GetSpeciesDiversityData's documented result in Go: each
// non-false-positive detection is bucketed on its own local calendar date in the
// datastore's zone, and the days within [startDate, endDate] report their distinct
// species count in date order.
func expectedDiversity(ds *Datastore, seeds []diversitySeed, startDate, endDate string) []datastore.DailyAnalyticsData {
	species := map[string]map[string]bool{}
	for _, s := range seeds {
		if s.falsePositive {
			continue
		}
		day := s.at.In(ds.timezone).Format(time.DateOnly)
		if (startDate != "" && day < startDate) || (endDate != "" && day > endDate) {
			continue
		}
		if species[day] == nil {
			species[day] = map[string]bool{}
		}
		species[day][s.species] = true
	}

	days := slices.Sorted(maps.Keys(species))
	result := make([]datastore.DailyAnalyticsData, 0, len(days))
	for _, day := range days {
		result = append(result, datastore.DailyAnalyticsData{Date: day, Count: len(species[day])})
	}
	return result
}

// TestV2OnlyDatastore_GetSpeciesDiversityData_RangeBoundaries pins the date range
// filter to the date buckets: a detection is included exactly when its bucketed
// date lies within the requested range. Detections sit on both sides of local
// midnight at each range edge and around both Europe/Helsinki DST changes, where
// a single offset for the whole range would differ from the true local offset.
func TestV2OnlyDatastore_GetSpeciesDiversityData_RangeBoundaries(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	assertDiversityRangeBoundaries(t, ds)
}

// assertDiversityRangeBoundaries seeds and checks the cases described on
// TestV2OnlyDatastore_GetSpeciesDiversityData_RangeBoundaries against an empty
// datastore, so the SQLite and MySQL tests share them.
func assertDiversityRangeBoundaries(t *testing.T, ds *Datastore) {
	t.Helper()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki
	ctx := t.Context()

	local := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2024, month, day, hour, minute, 0, 0, helsinki)
	}
	const (
		finch = "Fringilla coelebs"
		robin = "Erithacus rubecula"
		tit   = "Parus major"
	)
	// Spring forward 2024-03-31, fall back 2024-10-27.
	seeds := []diversitySeed{
		{species: finch, at: local(time.March, 24, 23, 59)},
		{species: robin, at: local(time.March, 25, 0, 0)},
		{species: tit, at: local(time.March, 25, 0, 30), falsePositive: true},
		{species: finch, at: local(time.March, 30, 23, 30)},
		{species: robin, at: local(time.March, 31, 2, 59)},
		{species: tit, at: local(time.March, 31, 4, 0)},
		{species: finch, at: local(time.April, 2, 23, 30)},
		{species: robin, at: local(time.April, 3, 0, 30)},
		{species: tit, at: local(time.April, 3, 1, 0)},
		{species: finch, at: local(time.October, 26, 23, 30)},
		{species: robin, at: local(time.October, 27, 0, 30)},
		{species: tit, at: local(time.October, 27, 3, 30)},
		{species: finch, at: local(time.October, 27, 23, 30)},
		{species: robin, at: local(time.October, 28, 0, 0)},
	}
	for _, s := range seeds {
		det := seedDetection(t, ds, s.species, s.at)
		if s.falsePositive {
			require.NoError(t, ds.manager.DB().Create(&entities.DetectionReview{
				DetectionID: det.ID,
				Verified:    entities.VerificationFalsePositive,
			}).Error)
		}
	}

	t.Run("end edge across spring forward uses the local date", func(t *testing.T) {
		got, err := ds.GetSpeciesDiversityData(ctx, "2024-03-25", "2024-04-02")
		require.NoError(t, err)
		assert.Equal(t, []datastore.DailyAnalyticsData{
			{Date: "2024-03-25", Count: 1},
			{Date: "2024-03-30", Count: 1},
			{Date: "2024-03-31", Count: 2},
			{Date: "2024-04-02", Count: 1},
		}, got)
	})

	boundaries := []string{
		"", "2024-03-24", "2024-03-25", "2024-03-30", "2024-03-31", "2024-04-01", "2024-04-02",
		"2024-04-03", "2024-10-26", "2024-10-27", "2024-10-28", "2024-12-31",
	}
	for _, start := range boundaries {
		for _, end := range boundaries {
			got, err := ds.GetSpeciesDiversityData(ctx, start, end)
			require.NoError(t, err, "range %q..%q", start, end)
			want := expectedDiversity(ds, seeds, start, end)
			if len(want) == 0 {
				assert.Empty(t, got, "range %q..%q", start, end)
				continue
			}
			assert.Equal(t, want, got, "range %q..%q", start, end)
		}
	}

	// Santiago skips local midnight in spring (2026-09-06 00:00 -04 becomes 01:00 -03), so the
	// first instant of a local date is not its midnight.
	t.Run("zone that skips midnight keeps each row on its own date", func(t *testing.T) {
		santiago, err := time.LoadLocation("America/Santiago")
		require.NoError(t, err)
		ds.timezone = santiago
		gapSeeds := []diversitySeed{
			{species: finch, at: time.Date(2026, time.September, 5, 23, 30, 0, 0, santiago)},
			{species: robin, at: time.Date(2026, time.September, 6, 1, 30, 0, 0, santiago)},
			{species: tit, at: time.Date(2026, time.September, 7, 0, 30, 0, 0, santiago)},
		}
		for _, s := range gapSeeds {
			seedDetection(t, ds, s.species, s.at)
		}
		for _, day := range []string{"2026-09-05", "2026-09-06", "2026-09-07"} {
			got, err := ds.GetSpeciesDiversityData(ctx, day, day)
			require.NoError(t, err)
			assert.Equal(t, []datastore.DailyAnalyticsData{{Date: day, Count: 1}}, got, day)
		}
	})

	t.Run("invalid dates are rejected", func(t *testing.T) {
		_, err := ds.GetSpeciesDiversityData(ctx, "2024-3-1", "")
		require.Error(t, err)
		_, err = ds.GetSpeciesDiversityData(ctx, "", "not-a-date")
		require.Error(t, err)
	})
}
