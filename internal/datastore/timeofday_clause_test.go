package datastore

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// clauseTestWindow tracks the production window (suncalc.SunEventWindow), the
// same value the SQL builder and the per-row classifier use, so the sweep cannot
// silently pass against a window that has drifted from production.
const clauseTestWindow = suncalc.SunEventWindow

// evalTimeClause interprets the WHERE fragments that buildTimeOfDayClause emits
// and reports whether a candidate "HH:MM:SS" time satisfies the clause. It knows
// only the handful of templates the builder produces; an unexpected one fails the
// test so a new template cannot slip through unverified.
// clauseTermRE matches one conjunct of a time-of-day clause: an optional "NOT "
// followed by a parenthesized notes.time predicate. buildTimeOfDayClause emits
// clauses as an AND-joined list of such terms.
var clauseTermRE = regexp.MustCompile(`(NOT )?\(([^()]+)\)`)

func evalTimeClause(t *testing.T, query string, args []any, candidate string) bool {
	t.Helper()
	const datePrefix = "notes.date = ? AND "
	require.Truef(t, strings.HasPrefix(query, datePrefix), "clause must start with the date guard: %q", query)
	expr := strings.TrimPrefix(query, datePrefix)

	// args[0] is the date (always true for the sweep's single date); the rest are
	// the notes.time bounds, consumed left to right by the fragments.
	require.GreaterOrEqual(t, len(args), 1, "clause must carry at least the date arg")
	timeArgs := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		s, ok := a.(string)
		require.True(t, ok, "time bound arg must be a string")
		timeArgs = append(timeArgs, s)
	}

	// The clause is a conjunction of optionally-negated parenthesized fragments;
	// evaluate each and AND them together, consuming bounds left to right.
	terms := clauseTermRE.FindAllStringSubmatch(expr, -1)
	require.NotEmptyf(t, terms, "no clause terms parsed from %q", expr)
	idx := 0
	result := true
	for _, term := range terms {
		val := evalTimeFragment(t, term[2], timeArgs, &idx, candidate)
		if term[1] == "NOT " {
			val = !val
		}
		result = result && val
	}
	require.Equalf(t, len(timeArgs), idx, "not all time bounds consumed for %q", query)
	return result
}

// evalTimeFragment evaluates one notes.time predicate fragment (the interior of a
// parenthesized term) against candidate, consuming its two bounds from timeArgs
// starting at *idx. It recognizes only the shapes buildTimeOfDayClause emits; an
// unexpected one fails the test so a new shape cannot slip through unverified.
func evalTimeFragment(t *testing.T, frag string, timeArgs []string, idx *int, candidate string) bool {
	t.Helper()
	require.GreaterOrEqualf(t, len(timeArgs), *idx+2, "fragment %q needs two bounds", frag)
	lo, hi := timeArgs[*idx], timeArgs[*idx+1]
	*idx += 2
	switch frag {
	case "notes.time >= ? AND notes.time <= ?": // inclusive window, no wrap
		return candidate >= lo && candidate <= hi
	case "notes.time >= ? OR notes.time <= ?": // inclusive window, wraps midnight
		return candidate >= lo || candidate <= hi
	case "notes.time >= ? AND notes.time < ?": // forward arc [start,end), no wrap
		return candidate >= lo && candidate < hi
	case "notes.time >= ? OR notes.time < ?": // forward arc, wraps midnight
		return candidate >= lo || candidate < hi
	default:
		t.Fatalf("unexpected clause fragment: %q", frag)
		return false
	}
}

// TestBuildTimeOfDayClause_MatchesClassifier proves the SQL time-of-day filter
// selects exactly the same category as the per-row classifier
// suncalc.ClassifyTimeOfDay, for every clock minute across 24 hours, in several
// sun-event scenarios. The high-latitude scenarios exercise sunrise/sunset
// windows (and the lit span) that cross midnight, the case the earlier naive
// range test got wrong.
func TestBuildTimeOfDayClause_MatchesClassifier(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	const dateStr = "2025-07-15"
	base := time.Date(2025, 7, 15, 0, 0, 0, 0, loc)

	scenarios := []struct {
		name    string
		sunrise time.Duration
		sunset  time.Duration
	}{
		{"normal mid-latitude day", 6 * time.Hour, 21 * time.Hour},
		{"high-latitude sunset near midnight", 3 * time.Hour, 23*time.Hour + 50*time.Minute},
		{"high-latitude sunrise near midnight", 15 * time.Minute, 22 * time.Hour},
		{"short winter day", 9 * time.Hour, 15 * time.Hour},
		// Sub-60-minute day (near-total-night winter): the sunrise and sunset
		// windows overlap, exercising the sunset clause's sunrise-window exclusion
		// and the empty "day" set.
		{"sub-60min day, overlapping windows", 12 * time.Hour, 12*time.Hour + 40*time.Minute},
		// Just over 60-minute day: windows are disjoint but the "day" span is a
		// single minute wide, exercising the tight AND day clause.
		{"very short day, disjoint windows", 12 * time.Hour, 13*time.Hour + 1*time.Minute},
		// Sub-60-minute night (polar summer, day > 23h): the sunrise and sunset
		// windows overlap across midnight, so the whole night is absorbed by the
		// transition windows and the night set is empty. Exercises the night
		// complement clause where a naive "outside the lit span" test would invert.
		{"sub-60min night, overlapping windows", 10 * time.Minute, 23*time.Hour + 50*time.Minute},
		// Summer inversion: local sunset falls after midnight (e.g. Reykjavik/Oslo
		// ~65N near the solstice, sunrise ~02:02, sunset ~00:01), so the daytime arc
		// wraps midnight and sunset's wall-clock precedes sunrise's. Midday must be
		// classified as day, not night.
		{"summer inversion, sunset after midnight", 2*time.Hour + 2*time.Minute, 1 * time.Minute},
		// More extreme inversion with a ~1h night: sunrise 02:00, sunset 01:00.
		{"summer inversion, short night", 2 * time.Hour, 1 * time.Hour},
	}

	categories := []string{TimeOfDaySunrise, TimeOfDaySunset, TimeOfDayDay, TimeOfDayNight}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			sun := &suncalc.SunEventTimes{Sunrise: base.Add(sc.sunrise), Sunset: base.Add(sc.sunset)}
			sunriseStr := sun.Sunrise.Format(time.TimeOnly)
			sunsetStr := sun.Sunset.Format(time.TimeOnly)
			sunriseStart := sun.Sunrise.Add(-clauseTestWindow).Format(time.TimeOnly)
			sunriseEnd := sun.Sunrise.Add(clauseTestWindow).Format(time.TimeOnly)
			sunsetStart := sun.Sunset.Add(-clauseTestWindow).Format(time.TimeOnly)
			sunsetEnd := sun.Sunset.Add(clauseTestWindow).Format(time.TimeOnly)

			for minute := range 24 * 60 {
				cand := base.Add(time.Duration(minute) * time.Minute)
				candStr := cand.Format(time.TimeOnly)
				want := suncalc.ClassifyTimeOfDay(cand, sun)

				for _, cat := range categories {
					q, a, ok := buildTimeOfDayClause(cat, dateStr, sunriseStr, sunsetStr, sunriseStart, sunriseEnd, sunsetStart, sunsetEnd)
					require.True(t, ok, "category %s must be recognized", cat)
					matched := evalTimeClause(t, q, a, candStr)
					if cat == want {
						assert.Truef(t, matched, "%s: %s should match category %q", sc.name, candStr, cat)
					} else {
						assert.Falsef(t, matched, "%s: %s should not match %q (classifier says %q)", sc.name, candStr, cat, want)
					}
				}
			}
		})
	}
}

// TestBuildTimeOfDayClause_UnknownCategory verifies an unrecognized category is
// reported as not-ok with no clause, so the caller skips it.
func TestBuildTimeOfDayClause_UnknownCategory(t *testing.T) {
	t.Parallel()
	q, a, ok := buildTimeOfDayClause("bogus", "2025-07-15", "06:00:00", "21:00:00", "05:30:00", "06:30:00", "20:30:00", "21:30:00")
	assert.False(t, ok)
	assert.Empty(t, q)
	assert.Nil(t, a)
}
