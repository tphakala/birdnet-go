package v2only

import "time"

// defaultSpeciesWindowHours is the window length, in hours, of a species drill-down that
// names an hour but no duration.
const defaultSpeciesWindowHours = 1

// localHourWindow returns the half-open Unix-second range [start, end) covering the
// local wall-clock hours from hour:00 up to (hour+hours):00 on the calendar date named
// by date (YYYY-MM-DD) in loc.
//
// The hours are the ones the clock showed, the same hours the dashboard buckets
// detections into, not elapsed hours from midnight. On a daylight saving change of
// exactly one hour, a repeated hour spans both of its occurrences and a skipped hour
// is empty. For a shift of a different length (a 30 minute shift leaves part of an hour
// present, and a 45 minute or two hour repeat is not contiguous with its hour) the
// window is the single range from the first occurrence of hour:00 to the first
// occurrence of (hour+hours):00. The end is clamped to the next local midnight, so a
// window never reaches into the next day. hours of zero or less yields an empty window
// (start == end).
//
// The range is half-open, so adjacent windows share a boundary without overlapping.
// date is validated before hour, so an invalid date reports the date error.
func localHourWindow(date, hour string, hours int, loc *time.Location) (start, end int64, err error) {
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return 0, 0, err
	}
	h, err := parseHour(hour)
	if err != nil {
		return 0, 0, err
	}
	y, m, d := day.Date()
	start = wallClockHourStart(y, m, d, h, loc).Unix()
	end = wallClockHourStart(y, m, d, min(h+hours, hoursPerDay), loc).Unix()
	return start, max(start, end), nil
}

// wallClockHourStart returns the first instant at which the wall clock in loc reads
// hour:00 or later on the given date. hour may be hoursPerDay, the next midnight.
//
// time.Date does not specify which instant it returns for a wall time that a zone
// transition skips or repeats, and the choice differs between zones (for 03:00 on
// 2026-10-25 in Europe/Helsinki it returns the second occurrence, for 01:00 on
// 2026-11-01 in America/New_York the first). Both cases are resolved here: a skipped
// time maps to the transition instant and a repeated time to its first occurrence.
func wallClockHourStart(year int, month time.Month, day, hour int, loc *time.Location) time.Time {
	t := time.Date(year, month, day, hour, 0, 0, 0, loc)
	want := time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
	got := wallClockAsUTC(t)
	zoneStart, zoneEnd := t.ZoneBounds()
	switch {
	case got.After(want):
		// Skipped by a forward jump; t lies just after it, at the start of its zone period.
		return zoneStart
	case got.Before(want):
		// Skipped by a forward jump; t lies just before it, at the end of its zone period.
		return zoneEnd
	}
	if zoneStart.IsZero() {
		return t // the zone has no earlier period
	}
	// Repeated: read with the previous period's offset, the same wall time falls before
	// this period starts, so it already occurred once in that period.
	_, prevOffset := zoneStart.Add(-time.Second).Zone()
	if first := time.Unix(want.Unix()-int64(prevOffset), 0); first.Before(zoneStart) {
		return first.In(loc)
	}
	return t
}

// wallClockAsUTC returns t's local date and clock reading as a UTC time, so two wall
// clock readings can be compared without zone offsets getting in the way.
func wallClockAsUTC(t time.Time) time.Time {
	y, m, d := t.Date()
	hh, mm, ss := t.Clock()
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}
