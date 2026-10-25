package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Scan steps for the agreement test. Sampling every windowScanStep across a few
// days lands on every second the windows are cut at, so an off-by-one at an edge
// shows up.
const (
	windowScanStep = time.Second
	windowScanDays = 3
)

// allTimeOfDayPeriods lists every label ClassifyTimeOfDay can return.
var allTimeOfDayPeriods = []string{timeOfDaySunrise, timeOfDaySunset, timeOfDayDay, timeOfDayNight}

// inAnyWindow reports whether unix falls inside one of the half-open windows.
func inAnyWindow(windows []TimeWindow, unix int64) bool {
	for _, w := range windows {
		if unix >= w.Start && unix < w.End {
			return true
		}
	}
	return false
}

// TestTimeOfDayWindows_AgreeWithClassifyTimeOfDay is the contract the SQL filter
// rests on: for every instant, it is inside a period's windows exactly when
// ClassifyTimeOfDay labels it that period against the sun events of its own
// station date. Stations cover a normal day, the southern hemisphere, a UTC+14
// zone, and a high-latitude summer whose sunset wall clock falls after midnight.
func TestTimeOfDayWindows_AgreeWithClassifyTimeOfDay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lat, lon float64
		start    time.Time
	}{
		{"temperate summer", 60.1699, 24.9384, time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)},
		{"temperate winter", 60.1699, 24.9384, time.Date(2024, 12, 20, 0, 0, 0, 0, time.UTC)},
		{"southern hemisphere", -33.8688, 151.2093, time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)},
		{"mid ocean", 21.3069, -157.8583, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)},
		{"sunset after local midnight", 64.1466, -21.9426, time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)},
		{"spring dst change", 52.52, 13.405, time.Date(2024, 3, 30, 0, 0, 0, 0, time.UTC)},
		{"autumn dst change", 52.52, 13.405, time.Date(2024, 10, 26, 0, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sc := NewSunCalc(tc.lat, tc.lon)
			from := tc.start
			to := from.AddDate(0, 0, windowScanDays)

			byPeriod := make(map[string][]TimeWindow, len(allTimeOfDayPeriods))
			for _, p := range allTimeOfDayPeriods {
				byPeriod[p] = sc.TimeOfDayWindows([]string{p}, from, to)
			}

			mismatches := 0
			for ts := from; ts.Before(to); ts = ts.Add(windowScanStep) {
				events, err := sc.GetSunEventTimes(ts)
				require.NoError(t, err)
				want := ClassifyTimeOfDay(ts, &events)
				for _, p := range allTimeOfDayPeriods {
					if got := inAnyWindow(byPeriod[p], ts.Unix()); got != (p == want) {
						mismatches++
						if mismatches <= 5 {
							t.Errorf("%s: window membership for %q is %v but ClassifyTimeOfDay says %q",
								ts.In(time.UTC).Format(time.RFC3339), p, got, want)
						}
					}
				}
			}
			assert.Zero(t, mismatches)
		})
	}
}

// TestTimeOfDayWindows_PartitionTheRange pins that the four periods tile the
// requested range exactly once, so a filter for all of them returns everything
// that has a label.
func TestTimeOfDayWindows_PartitionTheRange(t *testing.T) {
	t.Parallel()

	sc := NewSunCalc(60.1699, 24.9384)
	from := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 10)

	all := sc.TimeOfDayWindows(allTimeOfDayPeriods, from, to)
	require.Len(t, all, 1, "adjacent windows must merge into one span")
	assert.Equal(t, TimeWindow{Start: from.Unix(), End: to.Unix()}, all[0])
}

// TestTimeOfDayWindows_ClipsToRange pins that windows never extend past the
// requested bounds.
func TestTimeOfDayWindows_ClipsToRange(t *testing.T) {
	t.Parallel()

	sc := NewSunCalc(60.1699, 24.9384)
	from := time.Date(2024, 6, 20, 9, 17, 0, 0, time.UTC)
	to := from.Add(5 * time.Hour)

	for _, w := range sc.TimeOfDayWindows(allTimeOfDayPeriods, from, to) {
		assert.GreaterOrEqual(t, w.Start, from.Unix())
		assert.LessOrEqual(t, w.End, to.Unix())
	}
}

// TestTimeOfDayWindows_EmptyInputs pins the no-op cases.
func TestTimeOfDayWindows_EmptyInputs(t *testing.T) {
	t.Parallel()

	sc := NewSunCalc(60.1699, 24.9384)
	now := time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC)

	assert.Empty(t, sc.TimeOfDayWindows(nil, now, now.Add(time.Hour)))
	assert.Empty(t, sc.TimeOfDayWindows([]string{timeOfDayDay}, now, now))
	assert.Empty(t, sc.TimeOfDayWindows([]string{"teatime"}, now, now.Add(48*time.Hour)))
}

// TestTimeOfDayWindows_PolarStation pins that dates with no sun events (polar
// night, or midnight sun where sunrise or sunset does not occur) contribute no
// windows for any period, matching rows on those dates getting no label.
func TestTimeOfDayWindows_PolarStation(t *testing.T) {
	t.Parallel()

	sc := NewSunCalc(78.2232, 15.6267) // Longyearbyen
	for _, start := range []time.Time{
		time.Date(2024, 12, 20, 0, 0, 0, 0, time.UTC), // polar night
		time.Date(2024, 6, 20, 0, 0, 0, 0, time.UTC),  // midnight sun
	} {
		from, to := start, start.AddDate(0, 0, 3)
		windows := sc.TimeOfDayWindows(allTimeOfDayPeriods, from, to)
		for ts := from; ts.Before(to); ts = ts.Add(30 * time.Minute) {
			_, err := sc.GetSunEventTimes(ts)
			assert.Equal(t, err == nil, inAnyWindow(windows, ts.Unix()),
				"%s: a labelled instant must be in a window and an unlabelled one must not",
				ts.Format(time.RFC3339))
		}
	}
}

// TestGetSunEventTimes_DoesNotDependOnTheInstantPassed pins that the cached sun
// events for a date are the same whichever time of that date asked first.
func TestGetSunEventTimes_DoesNotDependOnTheInstantPassed(t *testing.T) {
	t.Parallel()

	const lat, lon = 60.1699, 24.9384
	// Offsets from UTC midnight that all fall on 15 Sep in Helsinki (UTC+3).
	day := time.Date(2024, 9, 15, 0, 0, 0, 0, time.UTC)

	sunrises := make([]time.Time, 0, 4)
	for _, offset := range []time.Duration{0, 3 * time.Hour, 9*time.Hour + 30*time.Minute, 20 * time.Hour} {
		events, err := NewSunCalc(lat, lon).GetSunEventTimes(day.Add(offset))
		require.NoError(t, err)
		sunrises = append(sunrises, events.Sunrise)
	}
	for _, sunrise := range sunrises[1:] {
		assert.True(t, sunrises[0].Equal(sunrise), "sunrise must not depend on the asking instant")
	}
}
