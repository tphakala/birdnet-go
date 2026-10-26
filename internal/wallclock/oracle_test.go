package wallclock_test

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// The oracle answers the wall-clock questions from the definition, using only the offsets
// time.Time.Zone reports, never ZoneBounds or time.Date, which are the pieces wallclock has to
// work around:
//
//	wall(u) = u + offset(u)
//	At(target) = min{u : wall(u) >= target}
//
// Transitions are found by sampling the offset every oracleSampleStep and bisecting, so the
// oracle does not depend on the zone data structure either.

const (
	secondsPerMinute = 60
	secondsPerHour   = 3600
	secondsPerDay    = 86400

	// oracleSampleStep is how often the offset is sampled to find transitions. No zone has two
	// transitions that close together.
	oracleSampleStep = 6 * secondsPerHour

	// oracleDaysBefore and oracleDaysAfter bound the wall-clock targets checked around a
	// transition: from a day before the transition's date to two days after it.
	oracleDaysBefore = 1
	oracleDaysAfter  = 2

	// oracleEdgeMargin keeps the checked transitions away from the ends of the sampled span, where
	// the oracle has no segment to answer from.
	oracleEdgeMargin = 5 * secondsPerDay

	// scanHalfWindow is the half width of the per-minute scans around each transition.
	scanHalfWindow = 30 * secondsPerHour
	// scanStep is the step of the bucket agreement scan.
	scanStep = 15 * secondsPerMinute
	// segmentScanHalfWindow is the half width of the per-minute segment offset check.
	segmentScanHalfWindow = 6 * secondsPerHour

	hoursPerDay = 24

	// The year spans of the two tiers. The all-zones span is the largest that keeps the test
	// inside its CPU budget under -race; the quirky zones are covered from 1970.
	tier1FromYear = 1970
	// bruteFromYear is the first year whose transitions are also cross-checked against the
	// minute-by-minute scan; the oracle's logic does not change with the year, the scan is slow.
	bruteFromYear = 2020
	tier1ToYear   = 2040
	tier2FromYear = 2020
	tier2ToYear   = 2040

	firstLeapYear = 1972
	lastLeapYear  = 2096
	leapYearStep  = 4
)

// tier1ZoneNames are the zones with known quirks: half-hour and 45 minute shifts, a two hour
// shift, skipped midnights, a skipped date, a 24 hour jump, fractional offsets and
// transitions that cross midnight.
var tier1ZoneNames = []string{
	"Europe/Helsinki", "America/New_York", "Australia/Sydney", "Australia/Lord_Howe",
	"Pacific/Norfolk", "Pacific/Chatham", "Antarctica/Troll", "Asia/Kolkata", "Asia/Kathmandu",
	"America/Havana", "America/Santiago", "Asia/Beirut", "Africa/Cairo", "America/Asuncion",
	"Pacific/Apia", "Pacific/Kiritimati", "America/St_Johns",
}

func unixDate(year int, month time.Month, day int) int64 {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Unix()
}

func offsetAt(loc *time.Location, u int64) int {
	_, off := time.Unix(u, 0).In(loc).Zone()
	return off
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// findTransitions returns every instant u in (from, to] at which the offset differs from the
// one a second earlier.
func findTransitions(loc *time.Location, from, to int64) []int64 {
	var out []int64
	prev, prevOff := from, offsetAt(loc, from)
	for u := from; u < to; {
		u = min(u+oracleSampleStep, to)
		off := offsetAt(loc, u)
		if off != prevOff {
			lo, hi := prev, u
			for hi-lo > 1 {
				mid := lo + (hi-lo)/2
				if offsetAt(loc, mid) == prevOff {
					lo = mid
				} else {
					hi = mid
				}
			}
			out = append(out, hi)
		}
		prev, prevOff = u, off
	}
	return out
}

// oracleSegments tiles [from, to) into runs of constant offset.
func oracleSegments(loc *time.Location, from, to int64) []wallclock.Segment {
	bounds := append([]int64{from}, findTransitions(loc, from, to)...)
	bounds = append(bounds, to)
	segs := make([]wallclock.Segment, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		segs = append(segs, wallclock.Segment{Start: bounds[i], End: bounds[i+1], OffsetSeconds: offsetAt(loc, bounds[i])})
	}
	return segs
}

// oracleAt returns min{u : wall(u) >= target} over segs, target being a wall reading in
// seconds since the Unix epoch. Inside one segment wall(u) = u + offset, so the first such u of
// a segment is max(start, target - offset); the first segment that has one holds the minimum.
func oracleAt(segs []wallclock.Segment, target int64) (int64, bool) {
	for _, s := range segs {
		if u := max(s.Start, target-int64(s.OffsetSeconds)); u < s.End {
			return u, true
		}
	}
	return 0, false
}

// bruteResolve answers sorted wall-clock targets by scanning instants minute by minute from
// start and refining the instant per second, the slow definition of oracleAt. -1 marks a target
// not reached before end.
func bruteResolve(loc *time.Location, targets []int64, start, end int64) []int64 {
	res := make([]int64, len(targets))
	for i := range res {
		res[i] = -1
	}
	next := 0
	for u := start; u <= end && next < len(targets); u += secondsPerMinute {
		wall := u + int64(offsetAt(loc, u))
		for next < len(targets) && wall >= targets[next] {
			first := u
			for s := u - secondsPerMinute + 1; s < u; s++ {
				if s+int64(offsetAt(loc, s)) >= targets[next] {
					first = s
					break
				}
			}
			res[next] = first
			next++
		}
	}
	return res
}

// dateOfWall returns the date, hour and minute that a wall reading in epoch seconds shows.
func dateOfWall(wall int64) (d wallclock.Date, hour, minute int) {
	t := time.Unix(wall, 0).UTC()
	return wallclock.DateOf(t), t.Hour(), t.Minute()
}

// targetsAround returns the wall readings every stepSeconds from oracleDaysBefore days before
// to oracleDaysAfter days after the date of the wall reading w.
func targetsAround(w, stepSeconds int64) []int64 {
	day := floorDiv(w, secondsPerDay) * secondsPerDay
	first, last := day-oracleDaysBefore*secondsPerDay, day+oracleDaysAfter*secondsPerDay
	out := make([]int64, 0, (last-first)/stepSeconds+1)
	for t := first; t < last; t += stepSeconds {
		out = append(out, t)
	}
	return out
}

// zoneCheck selects what verifyZone asserts and over which years.
type zoneCheck struct {
	fromYear, toYear int
	targetStep       int64 // seconds between checked wall-clock targets
	deep             bool  // brute-force cross-check, bucket agreement and per-minute segment offsets
}

// verifyZone checks wallclock against the oracle for every transition of loc in the years of cfg.
func verifyZone(t *testing.T, loc *time.Location, cfg zoneCheck) {
	t.Helper()
	from := unixDate(cfg.fromYear, time.January, 1)
	to := unixDate(cfg.toYear+1, time.January, 1)
	trs := findTransitions(loc, from, to)
	segs := oracleSegments(loc, from, to)

	// Segments over the whole span equal the oracle's: exact tiling, merged, at the found
	// transitions only.
	require.Equal(t, segs, wallclock.Segments(loc, from, to), "Segments over %d-%d", cfg.fromYear, cfg.toYear)

	for _, tr := range trs {
		if tr-from < oracleEdgeMargin || to-tr < oracleEdgeMargin {
			continue
		}
		wall := tr + int64(offsetAt(loc, tr))
		targets := targetsAround(wall, cfg.targetStep)

		for _, target := range targets {
			want, ok := oracleAt(segs, target)
			require.True(t, ok)
			d, h, m := dateOfWall(target)
			got := wallclock.At(d, h, m, 0, loc)
			if !assert.Equal(t, want, got.Unix(), "At(%s %02d:%02d) near transition %d", d, h, m, tr) {
				return
			}
			assert.Same(t, loc, got.Location())
		}

		if cfg.deep && time.Unix(tr, 0).UTC().Year() >= bruteFromYear {
			brute := bruteResolve(loc, targets, tr-3*secondsPerDay, tr+4*secondsPerDay)
			for i, target := range targets {
				want, _ := oracleAt(segs, target)
				if !assert.Equal(t, want, brute[i], "oracle vs brute force for wall %d near transition %d", target, tr) {
					return
				}
			}
		}

		verifyTiling(t, loc, wall)
		if cfg.deep {
			verifyBucketAgreement(t, loc, tr, trs)
			verifySegmentOffsets(t, loc, tr)
		}
		if t.Failed() {
			return
		}
	}
}

// verifyTiling checks that windows tile time for the dates around the wall reading w: day windows are
// contiguous and each is tiled by its 24 one-hour windows.
func verifyTiling(t *testing.T, loc *time.Location, w int64) {
	t.Helper()
	d0, _, _ := dateOfWall(w)
	for off := -oracleDaysBefore; off <= oracleDaysAfter-1; off++ {
		d := d0.AddDays(off)
		start, end := wallclock.DayBounds(d, loc)
		nextStart, _ := wallclock.DayBounds(d.AddDays(1), loc)
		assert.LessOrEqual(t, start.Unix(), end.Unix(), "DayBounds(%s) is not reversed", d)
		assert.Equal(t, end.Unix(), nextStart.Unix(), "DayBounds(%s).end == DayBounds(next).start", d)

		cursor := start.Unix()
		for h := range hoursPerDay {
			hs, he := wallclock.HourWindow(d, h, 1, loc)
			assert.Equal(t, cursor, hs.Unix(), "HourWindow(%s,%d) starts where the previous one ended", d, h)
			assert.LessOrEqual(t, hs.Unix(), he.Unix())
			cursor = he.Unix()
		}
		assert.Equal(t, end.Unix(), cursor, "the hours of %s tile its day", d)
	}
}

// repeatedWithin reports whether every backward shift among trs that lies in [lo, hi] repeats a
// wall-clock interval inside one unit (an hour or a date) of the given length in seconds. That
// is the exact condition under which a row's wall-clock bucket and its window agree
// (see the package documentation).
func repeatedWithin(loc *time.Location, trs []int64, lo, hi, unit int64) bool {
	for _, tr := range trs {
		if tr < lo || tr > hi {
			continue
		}
		before, after := int64(offsetAt(loc, tr-1)), int64(offsetAt(loc, tr))
		if after >= before {
			continue
		}
		// The wall clock goes from tr+before back to tr+after: [tr+after, tr+before) occurs twice.
		if floorDiv(tr+after, unit) != floorDiv(tr+before-1, unit) {
			return false
		}
	}
	return true
}

// verifyBucketAgreement checks that buckets agree with windows around transition tr: the instant of every scanned
// minute lies in the window of its own wall-clock hour and date exactly when every backward
// shift nearby repeats an interval inside one hour (one date).
func verifyBucketAgreement(t *testing.T, loc *time.Location, tr int64, trs []int64) {
	t.Helper()
	lo, hi := tr-scanHalfWindow-secondsPerDay, tr+scanHalfWindow+secondsPerDay
	hourHolds := repeatedWithin(loc, trs, lo, hi, secondsPerHour)
	dayHolds := repeatedWithin(loc, trs, lo, hi, secondsPerDay)

	var hourMiss, dayMiss int
	for u := tr - scanHalfWindow; u <= tr+scanHalfWindow; u += scanStep {
		wall := u + int64(offsetAt(loc, u))
		d, h, _ := dateOfWall(wall)
		if s, e := wallclock.HourWindow(d, h, 1, loc); u < s.Unix() || u >= e.Unix() {
			hourMiss++
		}
		if s, e := wallclock.DayBounds(d, loc); u < s.Unix() || u >= e.Unix() {
			dayMiss++
		}
	}
	if hourHolds {
		assert.Zero(t, hourMiss, "hour buckets agree with HourWindow near transition %d", tr)
	} else {
		assert.Positive(t, hourMiss, "hour buckets disagree with HourWindow near transition %d", tr)
	}
	if dayHolds {
		assert.Zero(t, dayMiss, "date buckets agree with DayBounds near transition %d", tr)
	} else {
		assert.Positive(t, dayMiss, "date buckets disagree with DayBounds near transition %d", tr)
	}
}

// verifySegmentOffsets checks that Segments over a short window around tr gives the offset the
// zone reports at every minute.
func verifySegmentOffsets(t *testing.T, loc *time.Location, tr int64) {
	t.Helper()
	from, to := tr-segmentScanHalfWindow, tr+segmentScanHalfWindow
	segs := wallclock.Segments(loc, from, to)
	require.NotEmpty(t, segs)
	require.Equal(t, from, segs[0].Start)
	require.Equal(t, to, segs[len(segs)-1].End)
	for i := 1; i < len(segs); i++ {
		require.Equal(t, segs[i-1].End, segs[i].Start, "segments are contiguous")
		require.NotEqual(t, segs[i-1].OffsetSeconds, segs[i].OffsetSeconds, "neighbours are merged")
	}
	for u := from; u < to; u += secondsPerMinute {
		i := sort.Search(len(segs), func(i int) bool { return segs[i].End > u })
		require.Equal(t, offsetAt(loc, u), segs[i].OffsetSeconds, "offset at %d", u)
	}
	// The second before and at the transition fall on either side of a segment boundary.
	i := sort.Search(len(segs), func(i int) bool { return segs[i].End > tr })
	assert.Equal(t, tr, segs[i].Start, "a segment starts exactly at transition %d", tr)
}

func tier1Zones(t *testing.T) map[string]*time.Location {
	t.Helper()
	all := testutil.ToolchainZones(t)
	zones := map[string]*time.Location{"Test/RuleOnly": testutil.RuleOnlyLocation(t)}
	for _, name := range tier1ZoneNames {
		loc, ok := all[name]
		require.True(t, ok, "zone %s is in the toolchain database", name)
		zones[name] = loc
	}
	return zones
}

// TestOracleQuirkyZones compares wallclock with the oracle in every zone class with known
// quirks over 1970-2040, at every quarter hour, and cross-checks the oracle itself against a
// minute-by-minute scan.
func TestOracleQuirkyZones(t *testing.T) {
	t.Parallel()
	for name, loc := range tier1Zones(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			verifyZone(t, loc, zoneCheck{
				fromYear: tier1FromYear, toYear: tier1ToYear,
				targetStep: 15 * secondsPerMinute, deep: true,
			})
		})
	}
}

// TestOracleAllZones compares wallclock with the oracle in every zone of the toolchain's zone
// database, aliases included, at every hour.
func TestOracleAllZones(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)
	require.NotEmpty(t, zones)
	for name, loc := range zones {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			verifyZone(t, loc, zoneCheck{
				fromYear: tier2FromYear, toYear: tier2ToYear,
				targetStep: secondsPerHour,
			})
		})
	}
}

// TestOracleLeapYearEnd checks the last and first day of every leap year in every zone and in
// the rule-only zone, whose ZoneBounds does not advance on 31 December of a leap year: Segments
// across 30 December to 2 January match the oracle and DayBounds follow the definition.
func TestOracleLeapYearEnd(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)
	check := func(t *testing.T, loc *time.Location) {
		t.Helper()
		for year := firstLeapYear; year <= lastLeapYear; year += leapYearStep {
			from := unixDate(year, time.December, 29)
			to := unixDate(year+1, time.January, 4)
			segs := oracleSegments(loc, from, to)
			spanFrom, spanTo := unixDate(year, time.December, 30), unixDate(year+1, time.January, 2)
			want := oracleSegments(loc, spanFrom, spanTo)
			require.Equal(t, want, wallclock.Segments(loc, spanFrom, spanTo), "Segments across the end of %d", year)

			for _, d := range []wallclock.Date{
				{Year: year, Month: time.December, Day: 31},
				{Year: year + 1, Month: time.January, Day: 1},
			} {
				start, end := wallclock.DayBounds(d, loc)
				wantStart, ok := oracleAt(segs, unixDate(d.Year, d.Month, d.Day))
				require.True(t, ok)
				wantEnd, ok := oracleAt(segs, unixDate(d.Year, d.Month, d.Day+1))
				require.True(t, ok)
				require.Equal(t, wantStart, start.Unix(), "start of %s", d)
				require.Equal(t, wantEnd, end.Unix(), "end of %s", d)
			}
		}
	}
	t.Run("rule-only zone", func(t *testing.T) {
		t.Parallel()
		check(t, testutil.RuleOnlyLocation(t))
	})
	for name, loc := range zones {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			check(t, loc)
		})
	}
}

// TestOracleSkippedAndRepeatedExamples pins the resolution rules on zones with repeated and skipped
// times, with literal expectations, so the oracle itself is not the only witness.
func TestOracleSkippedAndRepeatedExamples(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)
	tests := []struct {
		zone   string
		date   wallclock.Date
		hour   int
		minute int
		want   string
	}{
		{"Europe/Helsinki", wallclock.Date{Year: 2026, Month: time.October, Day: 25}, 3, 30, "2026-10-25T03:30:00+03:00"},
		{"America/New_York", wallclock.Date{Year: 2026, Month: time.November, Day: 1}, 1, 30, "2026-11-01T01:30:00-04:00"},
		{"Australia/Lord_Howe", wallclock.Date{Year: 2026, Month: time.April, Day: 5}, 1, 50, "2026-04-05T01:50:00+11:00"},
		{"Europe/Helsinki", wallclock.Date{Year: 2026, Month: time.March, Day: 29}, 3, 30, "2026-03-29T04:00:00+03:00"},
		{"America/Havana", wallclock.Date{Year: 2026, Month: time.March, Day: 8}, 0, 0, "2026-03-08T01:00:00-04:00"},
		{"America/Santiago", wallclock.Date{Year: 2026, Month: time.April, Day: 5}, 0, 0, "2026-04-05T00:00:00-04:00"},
		{"Pacific/Apia", wallclock.Date{Year: 2011, Month: time.December, Day: 30}, 12, 0, "2011-12-31T00:00:00+14:00"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s %02d:%02d", tt.zone, tt.date, tt.hour, tt.minute), func(t *testing.T) {
			t.Parallel()
			got := wallclock.At(tt.date, tt.hour, tt.minute, 0, zones[tt.zone])
			assert.Equal(t, tt.want, got.Format(time.RFC3339))
		})
	}
}
