//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// WallClockArithmetic detects hour and day arithmetic on instants that is
// wrong on the station's local calendar and points to internal/wallclock.
//
// Problematic patterns:
//
//	t.Truncate(time.Hour)              // wrong for +05:30, +05:45, -03:30
//	t.Round(24 * time.Hour)            // a UTC day, not a local day
//	int(d.Hours()) / 24                // a local day is 23 or 25 hours across DST
//	t.Add(n * 24 * time.Hour)          // lands on another wall time across DST
//
// Correct pattern: wallclock.FloorToHour, wallclock.DateOf, Date.AddDays,
// wallclock.DaysBetween and wallclock.CalendarDaysBetween. A genuine UTC
// bucket or an elapsed duration shown as days stays as is, with a
// //nolint:gocritic comment that gives the reason.
//
// The Add forms are c*time.Hour, n*c*time.Hour and c*time.Hour plus or minus
// an offset, with c equal to 24 or -24; a swapped operand order and
// 24*n*time.Hour are not matched. The receiver may be a time.Time or a
// *time.Time.
//
// Bare n*24*time.Hour durations (cache TTLs, retention windows) are not
// reported, only the ones added to a time.Time.
//
// The constant 24 is matched by value (Value.Int), so named constants such as
// hoursPerDay are covered. Value.Int does not evaluate a float constant, so the
// divisor of a float Hours() division is also matched by text: the literal 24
// or 24.0, or a name ending in HoursPerDay.
//
// Test files, the package internal/wallclock and every package named testutil
// (test support) are exempt.
func WallClockArithmetic(m dsl.Matcher) {
	// Truncate and Round to an hour or a day.
	m.Match(
		`$t.Truncate(time.Hour)`,
		`$t.Round(time.Hour)`,
	).
		Where((m["t"].Type.Is("time.Time") || m["t"].Type.Is("*time.Time")) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("Truncate/Round to an hour works on UTC time and is wrong for fractional offsets; use wallclock.FloorToHour (a Truncate replacement) from internal/wallclock")

	m.Match(
		`$t.Truncate($c * time.Hour)`,
		`$t.Round($c * time.Hour)`,
	).
		Where((m["t"].Type.Is("time.Time") || m["t"].Type.Is("*time.Time")) &&
			m["c"].Value.Int() == 24 &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("Truncate/Round to a day works on UTC time and is wrong for local days; use wallclock.DateOf or wallclock.DayBounds from internal/wallclock")

	// Day counts from hours.
	m.Match(
		`$d.Hours() / $c`,
		`int($d.Hours()) / $c`,
	).
		Where((m["c"].Value.Int() == 24 ||
			m["c"].Text.Matches(`^(24(\.0*)?|([A-Za-z]+\.)?[A-Za-z]*[Hh]oursPerDay)$`)) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("a day is not 24 hours across a DST change and Time.Sub saturates; count days with wallclock.DaysBetween or wallclock.CalendarDaysBetween from internal/wallclock")

	// Moving an instant by whole days.
	m.Match(
		`$t.Add($c * time.Hour)`,
		`$t.Add($c * time.Hour - $x)`,
		`$t.Add($c * time.Hour + $x)`,
		`$t.Add($n * $c * time.Hour)`,
	).
		Where((m["t"].Type.Is("time.Time") || m["t"].Type.Is("*time.Time")) &&
			(m["c"].Value.Int() == 24 || m["c"].Value.Int() == -24) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("adding 24-hour multiples to an instant does not move by calendar days across a DST change; use Date.AddDays or wallclock.At from internal/wallclock")
}
