package wallclock

import "time"

// FloorToHour returns the start of the wall-clock hour that contains t, shown in t's own UTC
// offset. The result is t minus its wall-clock minutes, seconds and nanoseconds, so it never
// falls after t and keeps the occurrence of a repeated DST hour that t is in, where time.Date
// would pick one of the two offsets on its own. time.Time.Truncate operates on absolute (UTC)
// time, so for offsets that are not a whole number of hours (+05:30, +05:45, -03:30) it floors
// to 30 or 45 minutes past the local hour. When the start lies before an offset change inside
// the same hour (a 30 minute DST shift such as Australia/Lord_Howe), it is a time in a fixed
// zone with t's offset instead of t's location, so it still reads as the start of t's
// wall-clock hour.
func FloorToHour(t time.Time) time.Time {
	sinceHourStart := time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second +
		time.Duration(t.Nanosecond())
	start := t.Add(-sinceHourStart)
	name, offset := t.Zone()
	if _, startOffset := start.Zone(); startOffset != offset {
		start = start.In(time.FixedZone(name, offset))
	}
	return start
}
