package v2only

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

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
	day, err := wallclock.ParseDate(date)
	if err != nil {
		return 0, 0, err
	}
	h, err := parseHour(hour)
	if err != nil {
		return 0, 0, err
	}
	startTime, endTime := wallclock.HourWindow(day, h, hours, loc)
	return startTime.Unix(), endTime.Unix(), nil
}
