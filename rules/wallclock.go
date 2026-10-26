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
// Matched forms:
//   - Truncate or Round of a time.Time or *time.Time to time.Hour, or to c
//     hours with c equal to 24 (in either operand order);
//   - Hours() divided by 24, with a time.Duration receiver (a literal 24, 24.0
//     or a name ending in HoursPerDay), and int(d.Hours()) / 24;
//   - Duration division d / (24 * time.Hour) or d / (time.Hour * 24);
//   - Add to a time.Time or *time.Time of c*time.Hour, time.Hour*c,
//     n*c*time.Hour, c*n*time.Hour, and c*time.Hour plus or minus an offset,
//     with c equal to 24 or -24.
//
// The constant 24 is matched by value (Value.Int), so named constants such as
// hoursPerDay are covered. Value.Int does not evaluate a float constant, so the
// divisor of a float Hours() division is matched by text instead.
//
// Not detected: a duration held in a variable (const day = 24 * time.Hour),
// multi-day constants other than 24 (48h, 72h, 168h), other operand orders
// (time.Hour*24*n, time.Hour*n*24), offsets on the left (24h + x - y), a
// receiver reached through an embedded struct, a method value, or a renamed
// time import. Bare n*24*time.Hour durations that are not added to a time.Time
// (cache TTLs, retention windows) are intentionally not reported.
//
// Test files, the package internal/wallclock and every package named testutil
// are exempt. Other test-support packages (such as conftest, securitytest)
// are not.
func WallClockArithmetic(m dsl.Matcher) {
	// Truncate and Round to an hour or a day.
	m.Match(
		`$t.Truncate(time.Hour)`,
	).
		Where((m["t"].Type.Is("time.Time") || m["t"].Type.Is("*time.Time")) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("Truncate to an hour works on UTC time and is wrong for fractional offsets; use wallclock.FloorToHour from internal/wallclock")

	m.Match(
		`$t.Round(time.Hour)`,
	).
		Where((m["t"].Type.Is("time.Time") || m["t"].Type.Is("*time.Time")) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("Round to an hour works on UTC time and is wrong for fractional offsets; wallclock.FloorToHour floors rather than rounds, so decide which is meant before replacing it (internal/wallclock)")

	m.Match(
		`$t.Truncate($c * time.Hour)`,
		`$t.Round($c * time.Hour)`,
		`$t.Truncate(time.Hour * $c)`,
		`$t.Round(time.Hour * $c)`,
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
		Where(m["d"].Type.Is("time.Duration") &&
			(m["c"].Value.Int() == 24 ||
				m["c"].Text.Matches(`^(24(\.0*)?|([A-Za-z]+\.)?[A-Za-z]*[Hh]oursPerDay)$`)) &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("a day is not 24 hours across a DST change and Time.Sub saturates; count days with wallclock.DaysBetween or wallclock.CalendarDaysBetween from internal/wallclock")

	// Whole-day counts by Duration division.
	m.Match(
		`$d / ($c * time.Hour)`,
		`$d / (time.Hour * $c)`,
	).
		Where(m["d"].Type.Is("time.Duration") &&
			m["c"].Value.Int() == 24 &&
			!m.File().Name.Matches(`_test\.go$`) &&
			!m.File().PkgPath.Matches(`(internal/wallclock|/testutil)$`)).
		Report("a day is not 24 hours across a DST change; count days with wallclock.DaysBetween or wallclock.CalendarDaysBetween from internal/wallclock")

	// Moving an instant by whole days.
	m.Match(
		`$t.Add($c * time.Hour)`,
		`$t.Add(time.Hour * $c)`,
		`$t.Add($c * $n * time.Hour)`,
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
