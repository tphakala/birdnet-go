package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestNightFilterExcludesSunriseSunsetWindows verifies that the "Night" time-of-day
// filter properly excludes the ±30 minute windows around sunrise and sunset.
// This test demonstrates issue #961 where sunrise/sunset detections were incorrectly
// included in "Night Only" searches.
//
// The test is timezone-agnostic and works in any timezone (UTC, local, etc.) by
// dynamically calculating test times based on actual sunrise/sunset for the test date.
func TestNightFilterExcludesSunriseSunsetWindows(t *testing.T) {
	// Setup test date and location.
	//
	// Two constraints to make the test truly timezone-agnostic:
	//
	// 1. **testDate must be UTC midnight, not Local.** The production filter
	//    parses filters.DateStart with time.Parse(time.DateOnly, ...) which
	//    returns UTC midnight. SunCalc caches by date.Equal() and
	//    astral.julianday() does date.UTC() and uses the UTC calendar day
	//    components. If the test passes a Local-midnight date, astral computes
	//    sunrise/sunset for a different UTC calendar day than the filter does
	//    (e.g. in NZST, Local-midnight is at noon UTC of the previous UTC day),
	//    yielding sub-minute drift on sunrise/sunset and off-by-1-second
	//    boundary failures.
	//
	// 2. **Longitude derived from local zone offset** so that solar noon at
	//    the test location ≈ local-clock noon. This keeps the location's
	//    sunrise and sunset both within the same local calendar day after
	//    SunCalc's ConvertUTCToLocal, regardless of where the test runs. With
	//    a fixed location far from the local timezone (e.g. London in CDT),
	//    sunrise's local-time string can lex-compare against sunset's in a
	//    way that breaks the filter's `time < sunriseStart OR time > sunsetEnd`
	//    logic.
	//
	// Mid-latitude (45° N) keeps day length moderate and avoids polar edge
	// cases (no sunrise/sunset in extreme latitudes around the solstices).
	testDate := time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC)
	_, offsetSec := testDate.In(time.Local).Zone()
	latitude := 45.0
	longitude := float64(offsetSec) / 3600.0 * 15.0

	// Create test database with location settings
	settings := &conf.Settings{}
	settings.BirdNET.Latitude = latitude
	settings.BirdNET.Longitude = longitude

	ds := createDatabase(t, settings)

	// Calculate actual sunrise/sunset times for this location and date
	sc := suncalc.NewSunCalc(latitude, longitude)
	sunTimes, err := sc.GetSunEventTimes(testDate)
	require.NoError(t, err, "Failed to calculate sun times")

	// Log the calculated times for debugging
	t.Logf("Test date: %s (timezone: %s)", testDate.Format(time.DateOnly), testDate.Location())
	t.Logf("Calculated sunrise: %s", sunTimes.Sunrise.Format(time.TimeOnly))
	t.Logf("Calculated sunset: %s", sunTimes.Sunset.Format(time.TimeOnly))

	// Define the 30-minute window
	window := 30 * time.Minute

	// Log the windows for debugging
	t.Logf("Sunrise window: %s to %s",
		sunTimes.Sunrise.Add(-window).Format(time.TimeOnly),
		sunTimes.Sunrise.Add(window).Format(time.TimeOnly))
	t.Logf("Sunset window: %s to %s",
		sunTimes.Sunset.Add(-window).Format(time.TimeOnly),
		sunTimes.Sunset.Add(window).Format(time.TimeOnly))

	// Dynamically generate test times based on calculated sunrise/sunset
	// This makes the test work in any timezone
	testCases := []struct {
		name          string
		timeOffset    time.Duration // Offset from a reference time
		reference     time.Time     // Reference time (sunrise or sunset)
		shouldBeNight bool
		description   string
	}{
		// Deep night cases - well outside any transition windows
		{
			name:          "Deep Night - 2h before sunrise",
			timeOffset:    -2 * time.Hour,
			reference:     sunTimes.Sunrise,
			shouldBeNight: true,
			description:   "Well before sunrise window, clearly night",
		},
		{
			name:          "Deep Night - 2h after sunset",
			timeOffset:    2 * time.Hour,
			reference:     sunTimes.Sunset,
			shouldBeNight: true,
			description:   "Well after sunset window, clearly night",
		},

		// Just before sunrise window
		{
			name:          "Before Sunrise Window - 31min before sunrise",
			timeOffset:    -31 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: true,
			description:   "Just before sunrise window begins, should still be night",
		},

		// Within sunrise window - should NOT be night
		{
			name:          "Sunrise Window Start - 30min before sunrise",
			timeOffset:    -30 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "At start of sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Window - 15min before sunrise",
			timeOffset:    -15 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Within sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Exact",
			timeOffset:    0,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Exact sunrise time, should NOT be night",
		},
		{
			name:          "Sunrise Window - 15min after sunrise",
			timeOffset:    15 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "Within sunrise window, should NOT be night",
		},
		{
			name:          "Sunrise Window End - 30min after sunrise",
			timeOffset:    30 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "At end of sunrise window, should NOT be night",
		},

		// After sunrise window - day time
		{
			name:          "Early Day - 31min after sunrise",
			timeOffset:    31 * time.Minute,
			reference:     sunTimes.Sunrise,
			shouldBeNight: false,
			description:   "After sunrise window, clearly day",
		},

		// Midday - definitely day
		{
			name:          "Midday",
			timeOffset:    0,
			reference:     sunTimes.Sunrise.Add(sunTimes.Sunset.Sub(sunTimes.Sunrise) / 2),
			shouldBeNight: false,
			description:   "Middle of the day, clearly not night",
		},

		// Before sunset window - still day
		{
			name:          "Late Day - 31min before sunset",
			timeOffset:    -31 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Before sunset window, still day",
		},

		// Within sunset window - should NOT be night
		{
			name:          "Sunset Window Start - 30min before sunset",
			timeOffset:    -30 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "At start of sunset window, should NOT be night",
		},
		{
			name:          "Sunset Window - 15min before sunset",
			timeOffset:    -15 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Within sunset window, should NOT be night",
		},
		{
			name:          "Sunset Exact",
			timeOffset:    0,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Exact sunset time, should NOT be night",
		},
		{
			name:          "Sunset Window - 15min after sunset",
			timeOffset:    15 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "Within sunset window, should NOT be night",
		},
		{
			name:          "Sunset Window End - 30min after sunset",
			timeOffset:    30 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: false,
			description:   "At end of sunset window, should NOT be night",
		},

		// After sunset window - night
		{
			name:          "Early Night - 31min after sunset",
			timeOffset:    31 * time.Minute,
			reference:     sunTimes.Sunset,
			shouldBeNight: true,
			description:   "After sunset window ends, should be night",
		},
	}

	// Insert test detections with dynamically calculated times
	dateStr := testDate.Format(time.DateOnly)
	insertedTimes := make(map[string]bool) // Track what we inserted

	for _, tc := range testCases {
		testTime := tc.reference.Add(tc.timeOffset)
		// Notes always record their wall clock in time.Local (see
		// datastore.NoteFromResult, which formats from a detection.Result.Timestamp
		// built from time.Now()), which is not necessarily the same time.Location as
		// sunTimes (SunCalc resolves its own location from the station's
		// coordinates). Converting here mirrors that real write path so the test
		// stays correct regardless of whether the two locations happen to agree.
		timeStr := testTime.In(time.Local).Format(time.TimeOnly)

		// Skip if we've already inserted this exact time (avoid duplicates)
		if insertedTimes[timeStr] {
			t.Logf("Skipping duplicate time: %s for test case: %s", timeStr, tc.name)
			continue
		}
		insertedTimes[timeStr] = true

		note := &Note{
			Date:           dateStr,
			Time:           timeStr,
			ScientificName: "Strix varia", // Barred Owl - a nocturnal species
			CommonName:     "Barred Owl",
			Confidence:     0.9,
		}
		err := ds.Save(note, []Results{})
		require.NoError(t, err, "Failed to insert test note for %s at %s", tc.name, timeStr)
		t.Logf("Inserted detection: %s at %s (should be night: %v)", tc.name, timeStr, tc.shouldBeNight)
	}

	// Test the Night filter using SearchDetections
	filters := &SearchFilters{
		TimeOfDay: TimeOfDayNight,
		DateStart: dateStr,
		DateEnd:   dateStr,
	}

	results, _, err := ds.SearchDetections(filters)
	require.NoError(t, err, "Failed to execute search with Night filter")

	// Create a map of returned timestamps for easy lookup
	returnedTimes := make(map[string]bool)
	for _, detection := range results {
		timeStr := detection.Timestamp.Format(time.TimeOnly)
		returnedTimes[timeStr] = true
		t.Logf("Night filter returned: %s", timeStr)
	}

	// Verify each test case
	expectedNightCount := 0
	for _, tc := range testCases {
		if tc.shouldBeNight {
			expectedNightCount++
		}
	}

	// Run subtests to verify Night filter behavior
	for _, tc := range testCases {
		testTime := tc.reference.Add(tc.timeOffset)
		timeStr := testTime.In(time.Local).Format(time.TimeOnly) // matches the write-side conversion above

		// Skip duplicate time checks
		if !insertedTimes[timeStr] {
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			wasReturned := returnedTimes[timeStr]

			if tc.shouldBeNight {
				assert.True(t, wasReturned,
					"%s (%s): Expected to be included in Night filter but was excluded. %s",
					tc.name, timeStr, tc.description)
			} else {
				assert.False(t, wasReturned,
					"%s (%s): Expected to be excluded from Night filter but was included. %s (THIS IS THE BUG FROM ISSUE #961)",
					tc.name, timeStr, tc.description)
			}
		})
	}

	// Log summary for context
	t.Logf("\n=== Test Summary ===")
	t.Logf("Night filter returned %d detections out of %d unique times inserted", len(results), len(insertedTimes))
	t.Logf("Expected %d night detections (2 deep night + 2 edge cases)", expectedNightCount)
	t.Log("\nThis test verifies issue #961 fix:")
	t.Log("The Night filter must exclude the ±30 minute windows around sunrise and sunset")
	t.Log("Before fix: Incorrectly included sunrise/sunset/day detections")
	t.Log("After fix: Correctly returns only true night detections")
}

// TestClassifyTimeOfDayMatchesDatastoreConstants guards against drift between the
// string values returned by suncalc.ClassifyTimeOfDay and the datastore.TimeOfDay*
// constants. suncalc is a leaf package and cannot import datastore, so the two
// sets of string literals are kept in lockstep by this assertion rather than a
// shared symbol.
func TestClassifyTimeOfDayMatchesDatastoreConstants(t *testing.T) {
	t.Parallel()
	base := time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC)
	sun := &suncalc.SunEventTimes{
		Sunrise: base.Add(6 * time.Hour),
		Sunset:  base.Add(21 * time.Hour),
	}
	assert.Equal(t, TimeOfDaySunrise, suncalc.ClassifyTimeOfDay(base.Add(6*time.Hour), sun))
	assert.Equal(t, TimeOfDaySunset, suncalc.ClassifyTimeOfDay(base.Add(21*time.Hour), sun))
	assert.Equal(t, TimeOfDayDay, suncalc.ClassifyTimeOfDay(base.Add(12*time.Hour), sun))
	assert.Equal(t, TimeOfDayNight, suncalc.ClassifyTimeOfDay(base.Add(2*time.Hour), sun))
}

// TestSunEventAnchorPicksTheMajorityStationDate pins down why the sun-event
// lookup is anchored at local noon rather than local midnight.
//
// notes.date is a server-local calendar date, but SunCalc re-derives the date in
// the station's coordinate-derived timezone, so one server-local date can span
// two station dates and no single lookup is right for all of its rows. Noon is
// the day's midpoint and therefore always resolves to the station date holding
// the larger share of them; midnight sits on the boundary and flips to the
// neighbouring date as soon as the station is behind the server - which is how
// the SQL filter came to bound a date's rows with another date's sunrise and
// sunset.
func TestSunEventAnchorPicksTheMajorityStationDate(t *testing.T) {
	t.Parallel()

	const dateStr = "2025-03-20"
	const samplesPerDay = 96 // one sample every 15 minutes

	// Spans the full range of real UTC offsets. Fixed zones keep the assertion
	// independent of the tzdata the test machine happens to carry.
	offsets := []int{-12, -10, -5, 0, 5, 10, 12, 14}

	// stationDateCoverage counts how many instants of the server-local day fall on
	// the given station-local date.
	stationDateCoverage := func(serverMidnight time.Time, stationZone *time.Location, stationDate string) int {
		covered := 0
		for i := range samplesPerDay {
			sample := serverMidnight.Add(time.Duration(i) * 24 * time.Hour / samplesPerDay)
			if sample.In(stationZone).Format(time.DateOnly) == stationDate {
				covered++
			}
		}
		return covered
	}

	for _, serverOffset := range offsets {
		for _, stationOffset := range offsets {
			serverZone := time.FixedZone("server", serverOffset*3600)
			stationZone := time.FixedZone("station", stationOffset*3600)

			serverMidnight, err := time.ParseInLocation(time.DateOnly, dateStr, serverZone)
			require.NoError(t, err)

			noonDate := sunEventAnchor(serverMidnight).In(stationZone).Format(time.DateOnly)
			noonCoverage := stationDateCoverage(serverMidnight, stationZone, noonDate)

			assert.GreaterOrEqual(t, noonCoverage, samplesPerDay/2,
				"server UTC%+d / station UTC%+d: the anchored station date %s must cover at least half the server-local day",
				serverOffset, stationOffset, noonDate)

			// The anchor exists to beat midnight, so hold it to that directly.
			midnightDate := serverMidnight.In(stationZone).Format(time.DateOnly)
			midnightCoverage := stationDateCoverage(serverMidnight, stationZone, midnightDate)
			assert.GreaterOrEqual(t, noonCoverage, midnightCoverage,
				"server UTC%+d / station UTC%+d: noon anchoring must never cover less of the day than midnight anchoring",
				serverOffset, stationOffset)
		}
	}

	// The concrete shape of the original defect: a UTC server with a station ten
	// hours behind it. Midnight resolves to the previous station date; noon does
	// not.
	serverZone := time.FixedZone("server", 0)
	stationZone := time.FixedZone("station", -10*3600)
	serverMidnight, err := time.ParseInLocation(time.DateOnly, dateStr, serverZone)
	require.NoError(t, err)
	assert.Equal(t, "2025-03-19", serverMidnight.In(stationZone).Format(time.DateOnly),
		"midnight anchoring is expected to land on the previous station date here")
	assert.Equal(t, dateStr, sunEventAnchor(serverMidnight).In(stationZone).Format(time.DateOnly),
		"noon anchoring must stay on the server-local date here")
}

// TestTimeOfDayFilterAgreesWithClassifiedLabel is a regression test for the SQL
// time-of-day filter and the per-row classifier resolving different sun events
// for the same date.
//
// The filter bounds rows by notes.date using one set of sunrise/sunset times,
// while the label comes from suncalc.ClassifyTimeOfDay per row. The two used to
// derive those times differently - the filter from the date, the classifier from
// whichever row happened to be scanned first for that date - so they could land
// on sun events a day apart. Sunrise and sunset drift by a couple of minutes a
// day, which is enough for a detection near a transition to be returned by the
// "sunset" filter and then rendered with a "day" label, visibly inconsistent in
// the Search page's Time of Day column.
//
// Detections are therefore sampled minute-by-minute across the transition
// windows, where a divergence of even a minute changes the answer. Both stations
// sit far from any plausible server timezone (UTC-10 and UTC+13), so on any
// machine at least one of them exercises a large server/station offset and the
// cross-midnight mapping that goes with it.
func TestTimeOfDayFilterAgreesWithClassifiedLabel(t *testing.T) {
	stations := []struct {
		name      string
		latitude  float64
		longitude float64
	}{
		{"Honolulu (UTC-10)", 21.3069, -157.8583},
		{"Auckland (UTC+13)", -36.8485, 174.7633},
	}

	// Near the equinox, so both stations get a sunrise and a sunset well inside
	// the local day - no polar edge cases, and all four categories are populated.
	targetDate := time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC)

	for _, station := range stations {
		t.Run(station.name, func(t *testing.T) {
			settings := &conf.Settings{}
			settings.BirdNET.Latitude = station.latitude
			settings.BirdNET.Longitude = station.longitude

			ds := createDatabase(t, settings)

			// Resolve the day's sun events from station-local noon, so the sample
			// times are derived independently of how production anchors its own
			// lookup - the point of the test is that the two production paths agree
			// with each other, not that they agree with the test.
			sc := suncalc.NewSunCalc(station.latitude, station.longitude)
			stationZone, err := time.LoadLocation(sc.LocationName())
			require.NoError(t, err, "station timezone %q must be loadable", sc.LocationName())
			stationNoon := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(),
				12, 0, 0, 0, stationZone)
			sunTimes, err := sc.GetSunEventTimes(stationNoon)
			require.NoError(t, err)

			// The transition windows are +/-SunEventWindow wide, so sampling a
			// little past them on both sides covers every boundary the filter and
			// the classifier each place, plus the day/night ground on either side.
			const sampleStep = time.Minute
			sampleSpan := suncalc.SunEventWindow + 5*time.Minute

			var sampleTimes []time.Time
			for _, event := range []time.Time{sunTimes.Sunrise, sunTimes.Sunset} {
				for offset := -sampleSpan; offset <= sampleSpan; offset += sampleStep {
					sampleTimes = append(sampleTimes, event.Add(offset))
				}
			}
			// Anchor the set with unambiguous midday and midnight rows too.
			sampleTimes = append(sampleTimes, stationNoon, stationNoon.Add(12*time.Hour))

			// Notes record their wall clock in time.Local (see NoteFromResult), which
			// need not be the station's zone - and converting can move a sample onto
			// the neighbouring local date, which is exactly the cross-midnight case
			// under test. Each note therefore carries its own local date, and the
			// query spans the three dates they can fall on.
			for i := range sampleTimes {
				local := sampleTimes[i].In(time.Local)
				note := &Note{
					Date:           local.Format(time.DateOnly),
					Time:           local.Format(time.TimeOnly),
					ScientificName: "Strix varia",
					CommonName:     "Barred Owl",
					Confidence:     0.9,
				}
				require.NoError(t, ds.Save(note, []Results{}),
					"failed to insert detection at %s", local.Format(time.RFC3339))
			}

			localMidday := stationNoon.In(time.Local)
			dateStart := localMidday.AddDate(0, 0, -1).Format(time.DateOnly)
			dateEnd := localMidday.AddDate(0, 0, 1).Format(time.DateOnly)

			categories := []string{TimeOfDayDay, TimeOfDayNight, TimeOfDaySunrise, TimeOfDaySunset}
			matched := 0
			for _, category := range categories {
				results, _, err := ds.SearchDetections(&SearchFilters{
					TimeOfDay: category,
					DateStart: dateStart,
					DateEnd:   dateEnd,
					PerPage:   len(sampleTimes) + 1,
				})
				require.NoError(t, err, "search with the %q filter failed", category)

				for i := range results {
					assert.Equal(t, category, results[i].TimeOfDay,
						"detection at %s was returned by the %q filter but labelled %q",
						results[i].Timestamp.Format(time.RFC3339), category, results[i].TimeOfDay)
				}
				matched += len(results)
				t.Logf("%s: %d of %d detections", category, len(results), len(sampleTimes))
			}

			// Every detection falls into exactly one category, so the four filters
			// must partition the inserted set. A shortfall would mean rows the
			// classifier labels are unreachable through the filter.
			assert.Equal(t, len(sampleTimes), matched,
				"the four time-of-day filters must together return every detection")
		})
	}
}

// TestSunEventAnchorIsStableAcrossDstTransitions guards the way the anchor is
// built. Adding 12h to a date's midnight lands at 11:00 or 13:00 on the two days
// a year the server's zone shifts, because those days are 23 or 25 hours long.
// Constructing noon on the calendar date directly keeps the anchor mid-day, and
// keeps it on the date it was asked about.
func TestSunEventAnchorIsStableAcrossDstTransitions(t *testing.T) {
	t.Parallel()

	// A zone with a well-known DST rule, independent of the test machine's own.
	zone, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// Spring forward (23-hour day) and fall back (25-hour day) in 2025.
	for _, dateStr := range []string{"2025-03-09", "2025-11-02"} {
		localDate, err := time.ParseInLocation(time.DateOnly, dateStr, zone)
		require.NoError(t, err)

		anchor := sunEventAnchor(localDate)
		assert.Equal(t, sunEventAnchorHour, anchor.Hour(),
			"%s: the anchor must be local noon, not midnight plus a fixed 12h", dateStr)
		assert.Equal(t, dateStr, anchor.Format(time.DateOnly),
			"%s: the anchor must stay on the date it was asked about", dateStr)
	}
}
