package wallclock

import "time"

// hoursPerDay is the number of hour windows in a date; HourStart accepts it as the next
// midnight.
const hoursPerDay = 24

// noonHour is the hour Noon anchors to.
const noonHour = 12

// At returns the first instant at which the wall clock in loc reads hour:min:sec on d or later,
// in loc: min{u : wall(u) >= d hour:min:sec}. A repeated time resolves to its first occurrence.
// A skipped time, of any length (including a skipped midnight and a wholly skipped date such as
// Pacific/Apia on 2011-12-30), resolves to the transition instant. Fields out of range
// normalise like time.Date (hour 24 is 00:00 of the next date).
//
// time.Date does not specify which instant it returns for a wall time that a zone transition
// skips or repeats, and the choice differs between zones (for 03:00 on 2026-10-25 in
// Europe/Helsinki it returns the second occurrence, for 01:00 on 2026-11-01 in
// America/New_York the first). At does not depend on that choice.
func At(d Date, hour, minute, second int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	t := time.Date(d.Year, d.Month, d.Day, hour, minute, second, 0, loc)
	want := time.Date(d.Year, d.Month, d.Day, hour, minute, second, 0, time.UTC)
	got := wallAsUTC(t)
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
	// Repeated: read with the previous period's offset, the same wall time falls before this
	// period starts, so it already occurred once in that period.
	_, prevOffset := zoneStart.Add(-time.Second).Zone()
	if first := time.Unix(want.Unix()-int64(prevOffset), 0); first.Before(zoneStart) {
		return first.In(loc)
	}
	return t
}

// HourStart returns the first instant at which the wall clock in loc reads hour:00 or later on
// d, see At. hour may be 24, the next midnight.
func HourStart(d Date, hour int, loc *time.Location) time.Time {
	return At(d, hour, 0, 0, loc)
}

// DayBounds returns the half-open range [start, end) of the instants whose wall-clock date in
// loc is d: start is HourStart(d, 0) and end is HourStart(d, 24), the start of the next date.
// Consecutive dates share a boundary, so day windows tile time without overlap. The range is
// empty for a date a zone skipped.
func DayBounds(d Date, loc *time.Location) (start, end time.Time) {
	return HourStart(d, 0, loc), HourStart(d, hoursPerDay, loc)
}

// HourWindow returns the half-open range [start, end) covering the local wall-clock hours from
// hour:00 up to (hour+hours):00 on d in loc.
//
// The hours are the ones the clock showed, the same hours the dashboard buckets detections
// into, not elapsed hours from midnight. On a daylight saving change of exactly one hour, a
// repeated hour spans both of its occurrences and a skipped hour is empty. For a shift of a
// different length (a 30 minute shift leaves part of an hour present, and a 45 minute or two
// hour repeat is not contiguous with its hour) the window is the single range from the first
// occurrence of hour:00 to the first occurrence of (hour+hours):00. The end is clamped to the
// next local midnight, so a window never reaches into the next date. hours of zero or less
// yields an empty window (start == end). hour must be 0 to 23: a negative hour starts on
// the previous date, and an hour of 24 or more gives an empty window.
//
// The range is half-open, so adjacent windows share a boundary without overlapping.
func HourWindow(d Date, hour, hours int, loc *time.Location) (start, end time.Time) {
	start = HourStart(d, hour, loc)
	end = HourStart(d, min(hour+hours, hoursPerDay), loc)
	if end.Before(start) {
		end = start
	}
	return start, end
}

// Noon returns 12:00:00 on the date of t, in t's own location. Date logic that anchors "today"
// on the local calendar day uses it: time.Time.Truncate operates on absolute (UTC) time, so
// truncating to a 24h boundary rounds to UTC midnight and yields the wrong date near the local
// day boundary on hosts with a non-zero UTC offset. Noon rather than midnight also sidesteps
// zones whose DST transition is at 00:00. Like At it is defined on every date.
func Noon(t time.Time) time.Time {
	return At(DateOf(t), noonHour, 0, 0, t.Location())
}

// wallAsUTC returns t's local date and clock reading as a UTC time, so two wall clock readings
// can be compared without zone offsets getting in the way.
func wallAsUTC(t time.Time) time.Time {
	y, m, d := t.Date()
	hh, mm, ss := t.Clock()
	return time.Date(y, m, d, hh, mm, ss, 0, time.UTC)
}
