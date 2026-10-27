package v2only

import (
	"math"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const (
	rangeSpeciesA    = "Fringilla coelebs"
	rangeSpeciesB    = "Erithacus rubecula"
	rangeSpeciesEdge = "Parus major"

	// The grid steps are prime minute counts, so successive rows fall at varying minutes of the
	// hour instead of repeating one minute.
	gridStepA = 37 * time.Minute
	gridStepB = 53 * time.Minute

	rangeSeedBatchSize  = 200
	rangeSeedsPerCase   = 500 // capacity estimate for the seeded rows of one case
	rangeSeedConfidence = 0.9
	topSpeciesLimit     = 10
	pairsPageDivisor    = 2
	openRangeLimit      = 100000
	noConfidenceFloor   = 0.0
)

// TestV2OnlyDatastore_BatchHourlyAcrossLeapYearEnd pins the dashboard path for a zone whose
// offset rule is resolved from its POSIX rule: the range crosses 31 December of a leap year,
// where Go reports a zone bound that does not advance, and the later spring change must still
// split the range.
func TestV2OnlyDatastore_BatchHourlyAcrossLeapYearEnd(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	loc := testutil.RuleOnlyLocation(t)
	ds.timezone = loc

	const (
		hourEET  = 10
		hourEEST = 6
	)
	seedDetection(t, ds, rangeSpeciesA, time.Date(2024, time.December, 30, hourEET, 30, 0, 0, loc))
	seedDetection(t, ds, rangeSpeciesA, time.Date(2025, time.January, 2, hourEET, 30, 0, 0, loc))
	// 06:30 EEST is 03:30 UTC.
	seedDetection(t, ds, rangeSpeciesA, time.Date(2025, time.April, 2, 3, 30, 0, 0, time.UTC))

	got, err := ds.GetBatchHourlyOccurrences(t.Context(), "2024-12-30", "2025-04-05", []string{rangeSpeciesA}, noConfidenceFloor)
	require.NoError(t, err)

	var want [hoursPerDay]int
	want[hourEET] = 2
	want[hourEEST] = 1
	assert.Equal(t, want, got[rangeSpeciesA])
}

// TestV2OnlyDatastore_RangeAnalyticsWallClock pins that hour and date buckets of range
// analytics follow the offset in effect at each detection's own instant, across daylight saving
// changes inside the range.
func TestV2OnlyDatastore_RangeAnalyticsWallClock(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	assertRangeAnalyticsWallClock(t, ds)
}

// rangeSeed is one seeded detection.
type rangeSeed struct {
	species string
	at      time.Time
}

// rangeCase is one zone and date range of assertRangeAnalyticsWallClock.
type rangeCase struct {
	name       string
	zone       string
	start, end string // inclusive local dates
	changes    []time.Time
}

// rangeCases lists the zones and ranges checked: both directions in the northern and southern
// hemispheres, a half-hour shift, a single fall-back day and a zone without daylight saving.
func rangeCases() []rangeCase {
	utc := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	return []rangeCase{
		{"helsinki spring", "Europe/Helsinki", "2026-03-25", "2026-04-02", []time.Time{utc(2026, time.March, 29, 1)}},
		{"helsinki fall", "Europe/Helsinki", "2026-10-20", "2026-10-26", []time.Time{utc(2026, time.October, 25, 1)}},
		{"helsinki fall day", "Europe/Helsinki", "2026-10-25", "2026-10-25", []time.Time{utc(2026, time.October, 25, 1)}},
		{"sydney autumn", "Australia/Sydney", "2026-03-30", "2026-04-08", []time.Time{utc(2026, time.April, 4, 16)}},
		{"sydney spring", "Australia/Sydney", "2026-09-30", "2026-10-08", []time.Time{utc(2026, time.October, 3, 16)}},
		{"lord howe autumn", "Australia/Lord_Howe", "2026-04-01", "2026-04-08", []time.Time{utc(2026, time.April, 4, 15)}},
		{"tokyo control", "Asia/Tokyo", "2026-03-25", "2026-04-02", nil},
	}
}

// aroundChange are the offsets from an offset change at which edge rows are seeded.
var aroundChange = []time.Duration{
	-3 * time.Hour, -90 * time.Minute, -30 * time.Minute, -time.Second, 0, time.Second,
	30 * time.Minute, 90 * time.Minute, 3 * time.Hour,
}

// edgeInstants returns the instants of the edge species for a case: around every offset change,
// and 23:30, 00:30, 02:59 and 04:00 local on every day of the range plus one day either side.
func edgeInstants(changes []time.Time, loc *time.Location, first, last time.Time) []time.Time {
	var out []time.Time
	for _, change := range changes {
		for _, d := range aroundChange {
			out = append(out, change.Add(d))
		}
	}
	for day := first.AddDate(0, 0, -1); !day.After(last.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		y, m, d := day.Date()
		for _, clock := range [][2]int{{23, 30}, {0, 30}, {2, 59}, {4, 0}} {
			out = append(out, time.Date(y, m, d, clock[0], clock[1], 0, 0, loc))
		}
	}
	return out
}

// seedRangeDetections inserts the detections in batches.
func seedRangeDetections(t *testing.T, ds *Datastore, seeds []rangeSeed) {
	t.Helper()
	labels := map[string]uint{}
	rows := make([]entities.Detection, 0, len(seeds))
	for _, s := range seeds {
		id, ok := labels[s.species]
		if !ok {
			label, err := ds.label.GetOrCreate(t.Context(), s.species, ds.defaultModelID, ds.speciesLabelTypeID, ds.avesClassID)
			require.NoError(t, err)
			id = label.ID
			labels[s.species] = id
		}
		rows = append(rows, entities.Detection{
			ModelID: ds.defaultModelID, LabelID: id, DetectedAt: s.at.Unix(), Confidence: rangeSeedConfidence,
		})
	}
	require.NoError(t, ds.manager.DB().CreateInBatches(&rows, rangeSeedBatchSize).Error)
}

// rangeExpectation is the reference result of a range, computed from the raw instants.
type rangeExpectation struct {
	hours      [hoursPerDay]int
	hoursBySp  map[string][hoursPerDay]int
	dates      map[string]int
	pairs      []datastore.SpeciesDetectionDate
	rowsInSpan int
}

// expectRange buckets the seeds in [startUnix, endUnix) by their own wall clock in loc.
func expectRange(seeds []rangeSeed, loc *time.Location, startUnix, endUnix int64, only string) rangeExpectation {
	exp := rangeExpectation{hoursBySp: map[string][hoursPerDay]int{}, dates: map[string]int{}}
	seenPair := map[[2]string]bool{}
	for _, s := range seeds {
		if s.at.Unix() < startUnix || s.at.Unix() >= endUnix {
			continue
		}
		local := s.at.In(loc)
		if only == "" || s.species == only {
			exp.hours[local.Hour()]++
			exp.dates[local.Format(time.DateOnly)]++
			exp.rowsInSpan++
		}
		bySp := exp.hoursBySp[s.species]
		bySp[local.Hour()]++
		exp.hoursBySp[s.species] = bySp
		key := [2]string{s.species, local.Format(time.DateOnly)}
		if !seenPair[key] {
			seenPair[key] = true
			exp.pairs = append(exp.pairs, datastore.SpeciesDetectionDate{ScientificName: s.species, Date: key[1]})
		}
	}
	sort.Slice(exp.pairs, func(i, j int) bool {
		if exp.pairs[i].Date != exp.pairs[j].Date {
			return exp.pairs[i].Date < exp.pairs[j].Date
		}
		return exp.pairs[i].ScientificName < exp.pairs[j].ScientificName
	})
	return exp
}

// assertRangeAnalyticsWallClock seeds each case of rangeCases into one datastore and checks the
// range analytics against expectRange. The SQLite and MySQL tests share it.
func assertRangeAnalyticsWallClock(t *testing.T, ds *Datastore) {
	t.Helper()
	ctx := t.Context()
	seeded := make([]rangeSeed, 0, rangeSeedsPerCase*len(rangeCases()))
	total := 0

	for _, c := range rangeCases() {
		loc, err := time.LoadLocation(c.zone)
		require.NoError(t, err)
		ds.timezone = loc
		first, err := time.ParseInLocation(time.DateOnly, c.start, loc)
		require.NoError(t, err)
		last, err := time.ParseInLocation(time.DateOnly, c.end, loc)
		require.NoError(t, err)
		startUnix, endUnix := first.Unix(), last.AddDate(0, 0, 1).Unix()

		var fresh []rangeSeed
		for at := first; at.Before(last.AddDate(0, 0, 1)); at = at.Add(gridStepA) {
			fresh = append(fresh, rangeSeed{rangeSpeciesA, at})
		}
		for at := first; at.Before(last.AddDate(0, 0, 1)); at = at.Add(gridStepB) {
			fresh = append(fresh, rangeSeed{rangeSpeciesB, at})
		}
		for _, at := range edgeInstants(c.changes, loc, first, last) {
			fresh = append(fresh, rangeSeed{rangeSpeciesEdge, at})
		}
		seedRangeDetections(t, ds, fresh)
		seeded = append(seeded, fresh...)
		total += len(fresh)

		// One false positive that must not be counted anywhere.
		fp := seedDetection(t, ds, rangeSpeciesA, first.Add(time.Hour))
		require.NoError(t, ds.manager.DB().Create(&entities.DetectionReview{
			DetectionID: fp.ID, Verified: entities.VerificationFalsePositive,
		}).Error)

		t.Run(c.name, func(t *testing.T) {
			exp := expectRange(seeded, loc, startUnix, endUnix, "")
			require.NotZero(t, exp.rowsInSpan)

			dist, err := ds.GetHourlyDistribution(ctx, c.start, c.end, "")
			require.NoError(t, err)
			var gotHours [hoursPerDay]int
			prev := -1
			for _, d := range dist {
				assert.Greater(t, d.Hour, prev, "hours ascend")
				prev = d.Hour
				gotHours[d.Hour] = d.Count
			}
			assert.Equal(t, exp.hours, gotHours, "hourly distribution")

			daily, err := ds.GetDailyAnalyticsData(ctx, c.start, c.end, "")
			require.NoError(t, err)
			gotDates := map[string]int{}
			for _, d := range daily {
				gotDates[d.Date] = d.Count
			}
			assert.Equal(t, exp.dates, gotDates, "daily counts")
			assert.True(t, slices.IsSortedFunc(daily, func(a, b datastore.DailyAnalyticsData) int {
				return -compareStrings(a.Date, b.Date)
			}), "dates descend")

			succession, err := ds.GetAcousticSuccession(ctx, c.start, c.end, nil, topSpeciesLimit)
			require.NoError(t, err)
			gotBySpecies := map[string][hoursPerDay]int{}
			for i := range succession {
				gotBySpecies[succession[i].ScientificName] = succession[i].Counts
			}
			assert.Equal(t, exp.hoursBySp, gotBySpecies, "succession hours")

			pairs, err := ds.GetSpeciesDetectionDatesInPeriod(ctx, c.start, c.end, openRangeLimit, 0)
			require.NoError(t, err)
			gotPairs := make([]datastore.SpeciesDetectionDate, len(pairs))
			for i, p := range pairs {
				gotPairs[i] = datastore.SpeciesDetectionDate{ScientificName: p.ScientificName, Date: p.Date}
			}
			assert.Equal(t, exp.pairs, gotPairs, "species/date pairs")

			if c.name == "helsinki fall" {
				half := len(exp.pairs) / pairsPageDivisor
				page1, err := ds.GetSpeciesDetectionDatesInPeriod(ctx, c.start, c.end, half, 0)
				require.NoError(t, err)
				page2, err := ds.GetSpeciesDetectionDatesInPeriod(ctx, c.start, c.end, len(exp.pairs), half)
				require.NoError(t, err)
				assert.Equal(t, pairs, append(slices.Clone(page1), page2...), "pages concatenate to the full list")
			}

			if c.start == c.end {
				hourly, err := ds.GetHourlyAnalyticsData(ctx, c.start, "")
				require.NoError(t, err)
				var gotDay [hoursPerDay]int
				sum := 0
				for _, h := range hourly {
					gotDay[h.Hour] = h.Count
					sum += h.Count
				}
				assert.Equal(t, exp.hours, gotDay, "single day hourly")
				assert.Equal(t, exp.rowsInSpan, sum)

				edgeExp := expectRange(seeded, loc, startUnix, endUnix, rangeSpeciesEdge)
				require.NotZero(t, edgeExp.hoursBySp[rangeSpeciesEdge][hoursPerDay-1], "fixture has a 23:xx row")
				edgeHourly, err := ds.GetHourlyAnalyticsData(ctx, c.start, rangeSpeciesEdge)
				require.NoError(t, err)
				var gotEdge [hoursPerDay]int
				for _, h := range edgeHourly {
					gotEdge[h.Hour] = h.Count
				}
				assert.Equal(t, edgeExp.hoursBySp[rangeSpeciesEdge], gotEdge, "single day edge species")
			}
		})
	}

	t.Run("open range keeps every row", func(t *testing.T) {
		daily, err := ds.GetDailyAnalyticsData(ctx, "", "", "")
		require.NoError(t, err)
		sum := 0
		for _, d := range daily {
			sum += d.Count
		}
		assert.Equal(t, total, sum)
	})
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TestV2OnlyDatastore_ZoneSegmentsCoverTheWholeRange pins that the segments tile the requested
// range exactly even where the enumeration is bounded: an open start and end, a start before
// the epoch, and a range that starts beyond the bound.
func TestV2OnlyDatastore_ZoneSegmentsCoverTheWholeRange(t *testing.T) {
	t.Parallel()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds := &Datastore{timezone: helsinki}

	const (
		farFutureYears = 20   // far past the bound of one leap year after now
		spanSeconds    = 1000 // length of the short ranges
		unixEpoch      = 0
		// Helsinki changes offset twice a year, so any range of several years crosses more than
		// two offset periods; a short range beyond the bound has exactly one segment.
		minSegmentsAcrossYears = 3
		singleSegment          = 1
		emptyRangeInstant      = 10
	)
	pastEnd := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
	farFuture := time.Now().AddDate(farFutureYears, 0, 0).Unix()
	cases := []struct {
		name       string
		start, end int64
		minLen     int
	}{
		{"open start and end", unixEpoch, math.MaxInt64, minSegmentsAcrossYears},
		{"start before the epoch", unixEpoch - spanSeconds, pastEnd, minSegmentsAcrossYears},
		{"range beyond the bound", farFuture, farFuture + spanSeconds, singleSegment},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			segs := ds.zoneSegments(c.start, c.end)
			require.GreaterOrEqual(t, len(segs), c.minLen)
			assert.Equal(t, c.start, segs[0].Start, "first segment starts at the range start")
			assert.Equal(t, c.end, segs[len(segs)-1].End, "last segment ends at the range end")
			for i := 1; i < len(segs); i++ {
				assert.Equal(t, segs[i-1].End, segs[i].Start, "segments are contiguous")
			}
		})
	}

	t.Run("range beyond the bound uses the offset in effect at its start", func(t *testing.T) {
		segs := ds.zoneSegments(farFuture, farFuture+spanSeconds)
		require.Len(t, segs, singleSegment)
		_, want := time.Unix(farFuture, 0).In(helsinki).Zone()
		assert.Equal(t, want, segs[0].OffsetSeconds)
	})

	t.Run("empty range has no segments", func(t *testing.T) {
		assert.Empty(t, ds.zoneSegments(emptyRangeInstant, emptyRangeInstant))
	})
}
