package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// havanaZone is a zone whose spring-forward transition skips midnight: on 2026-03-08 the
	// clocks go from 23:59:59 straight to 01:00:00.
	havanaZone = "America/Havana"
	// skippedMidnightDay is the date of that transition in havanaZone.
	skippedMidnightDay = "2026-03-08"
	// skippedMidnightRangeStart and skippedMidnightRangeEnd are an inclusive range around it.
	skippedMidnightRangeStart = "2026-03-06"
	skippedMidnightRangeEnd   = "2026-03-10"

	// Station coordinates of Havana for the civil dawn test.
	havanaLatitude  = 23.1
	havanaLongitude = -82.4
)

var skippedMidnightDates = []string{"2026-03-06", "2026-03-07", "2026-03-08", "2026-03-09", "2026-03-10"}

func TestBuildActivityHeatmap_SkippedMidnightAxisHasEveryDateOnce(t *testing.T) {
	t.Parallel()

	loc := toolchainZone(t, havanaZone)
	timestamps := []int64{
		epochAt(loc, 2026, 3, 7, 12, 0),
		epochAt(loc, 2026, 3, 8, 12, 0),
		epochAt(loc, 2026, 3, 10, 12, 0),
	}

	got, err := buildActivityHeatmap(timestamps, loc, skippedMidnightRangeStart, skippedMidnightRangeEnd)
	require.NoError(t, err)

	assert.Equal(t, skippedMidnightDates, got.Dates, "five distinct dates in order across the skipped midnight")
	assert.Equal(t, []int{1, 2, 4}, got.CellDateIndex, "each detection lands on its own local date")
	assert.Equal(t, []int{1, 1, 1}, got.CellCount)
}

func TestBuildDailyActivityOnset_SkippedMidnightAxisAndDawnDate(t *testing.T) {
	t.Parallel()

	loc := toolchainZone(t, havanaZone)
	// Five detections at 05:00..05:40 on the skipped-midnight day: rank 3 gives onset 05:20.
	timestamps := make([]int64, 0, 5)
	for _, minute := range []int{0, 10, 20, 30, 40} {
		timestamps = append(timestamps, epochAt(loc, 2026, 3, 8, 5, minute))
	}
	// The fake civil dawn is the day of the month in minutes, so the relative onset shows which
	// date the lookup was asked for.
	dawn := func(date wallclock.Date) (int, bool) { return date.Day, true }

	days, err := buildDailyActivityOnset(timestamps, loc, skippedMidnightRangeStart, skippedMidnightRangeEnd,
		onsetDetectionRank, minOnsetDetections, dawn)
	require.NoError(t, err)

	gotDates := make([]string, 0, len(days))
	for _, d := range days {
		gotDates = append(gotDates, d.Date)
	}
	assert.Equal(t, skippedMidnightDates, gotDates)

	onset := onsetByDate(days)[skippedMidnightDay]
	assert.Equal(t, 5, onset.DetectionCount)
	require.NotNil(t, onset.OnsetRelMinutes)
	assert.Equal(t, 5*60+20-8, *onset.OnsetRelMinutes, "civil dawn is looked up for 2026-03-08")
}

func TestComputeYearOverYearWindows_SkippedMidnightAnchorsAndBounds(t *testing.T) {
	t.Parallel()

	loc := toolchainZone(t, havanaZone)
	// The transition instant: the day after 2026-03-07 starts when the clocks jump to 01:00.
	transition := time.Date(2026, time.March, 8, 1, 0, 0, 0, loc)

	t.Run("reference on the skipped-midnight date", func(t *testing.T) {
		t.Parallel()
		w := computeYearOverYearWindows(wallclock.Date{Year: 2026, Month: time.March, Day: 8}, loc)

		assert.Equal(t, 2026, w.curYear)
		assert.Equal(t, skippedMidnightDay, w.curEnd, "anchored on the reference date, not the day before")
		assert.Equal(t, "2025-03-08", w.priorEnd)
		assert.Equal(t, time.Date(2026, time.March, 9, 0, 0, 0, 0, loc).Unix(), w.curEndEpoch)
		assert.Equal(t, time.Date(2026, time.January, 1, 0, 0, 0, 0, loc).Unix(), w.curStartEpoch)
	})

	t.Run("day after the end date starts at the transition", func(t *testing.T) {
		t.Parallel()
		w := computeYearOverYearWindows(wallclock.Date{Year: 2026, Month: time.March, Day: 7}, loc)

		assert.Equal(t, transition.Unix(), w.curEndEpoch, "the exclusive end is the transition instant")
	})
}

func TestV2OnlyDatastore_GetYearOverYearAnchorsOnSkippedMidnightDate(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ds.timezone = toolchainZone(t, havanaZone)

	const species = "Fringilla coelebs"
	for _, at := range []string{
		"2026-03-07T23:30:00-05:00", // last half hour before the clocks jump
		"2026-03-08T06:00:00-04:00", // on the requested date
		"2026-03-09T06:00:00-04:00", // after it
		"2025-03-08T06:00:00-05:00", // same date a year earlier
	} {
		seedDetection(t, ds, species, mustRFC3339(t, at))
	}

	got, err := ds.GetYearOverYear(t.Context(), skippedMidnightDay)
	require.NoError(t, err)

	require.NotEmpty(t, got.Points)
	last := got.Points[len(got.Points)-1]
	assert.Equal(t, skippedMidnightDay, last.Date, "the series ends on the requested date")
	assert.Equal(t, "03-08", last.MonthDay)
	assert.Equal(t, 2, last.ThisYear, "detections up to and including the requested date")
	assert.Equal(t, 1, last.LastYear)

	// An empty date means today in the station zone and must still resolve to a window. The clock
	// is not injectable, so which date it picks is not asserted here.
	today, err := ds.GetYearOverYear(t.Context(), "")
	require.NoError(t, err)
	assert.NotEmpty(t, today.Points)

	_, err = ds.GetYearOverYear(t.Context(), "not-a-date")
	require.Error(t, err)
}

// TestCivilDawnMinuteLookup_ReturnsDawnOfTheRequestedDate pins that the lookup answers for the
// requested date (not a neighbouring one) and in the station zone. It does not distinguish a noon
// anchor from a midnight one: SunCalc resolves both to the same date in Havana.
func TestCivilDawnMinuteLookup_ReturnsDawnOfTheRequestedDate(t *testing.T) {
	t.Parallel()

	havana := toolchainZone(t, havanaZone)
	sun := suncalc.NewSunCalc(havanaLatitude, havanaLongitude)
	ds := &Datastore{timezone: havana, suncalc: sun}

	got, ok := ds.civilDawnMinuteLookup()(wallclock.Date{Year: 2026, Month: time.March, Day: 8})

	require.True(t, ok)
	want, wantOK := sun.GetCivilDawn(time.Date(2026, time.March, 8, 12, 0, 0, 0, havana))
	require.True(t, wantOK)
	wantLocal := want.In(havana)
	assert.Equal(t, wantLocal.Hour()*60+wantLocal.Minute(), got)
}
