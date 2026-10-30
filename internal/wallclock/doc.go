// Package wallclock converts between wall-clock time and instants for one *time.Location: calendar
// dates, wall time to instant, day and hour windows, hour floors, UTC offsets and offset
// segments. It imports only the standard time and iter packages, so any layer may use it.
//
// Every function that takes a location treats nil as time.Local, and none reads settings, so a
// caller keeps passing its location per call and settings changes take effect immediately.
//
// # Contract
//
//   - Wall time to instant (At, HourStart, DayBounds, HourWindow, Noon) resolves a wall reading to
//     the first instant at which the clock shows that time or later. A repeated time (clocks
//     going back) resolves to its first occurrence; a skipped time (clocks going forward, of any
//     length, including a skipped midnight or a wholly skipped date) resolves to the transition
//     instant. It never depends on the zone-specific choice time.Date makes. As a result the
//     results are non-decreasing in the wall time, day windows tile time exactly, and the 24
//     one-hour windows of a date tile its day window.
//   - Instant to wall time (DateOf, FloorToHour) keeps the occurrence of a repeated hour that the
//     instant is in.
//   - A bucket by wall-clock hour or date agrees with the window of the same hour or date when
//     every backward shift repeats a wall-clock interval that lies inside one hour (one date).
//     That holds for every one hour shift at a whole hour and for the 30 minute shifts of
//     Australia/Lord_Howe and Pacific/Norfolk. It does not hold for shifts that do not fit in an
//     hour, such as Pacific/Chatham (45 minutes), Antarctica/Troll (two hours) and shifts that
//     cross midnight (America/St_Johns before 2011). The window then covers the first occurrence
//     of the hour start to the first occurrence of the next one.
//   - Offsets are exact seconds, fractional offsets (+05:30, +05:45, -03:30) included. Arithmetic
//     is done on wall readings or Unix seconds, never with Truncate, and day counts use Unix
//     seconds, so time.Time.Sub (which saturates at about 292 years) is not involved.
//
// SQL, settings and the parsing of hour strings are not part of the package.
package wallclock
