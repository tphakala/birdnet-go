package wallclock

import (
	"iter"
	"time"
)

// secondsPerDay is the length of a day on the UTC calendar, which has no DST. Day counts use
// Unix seconds because time.Time.Sub saturates at about 292 years.
const secondsPerDay = 24 * 60 * 60

// Date is a calendar date without a time of day or a zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// DateOf returns the date of t read in t's own location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// ParseDate parses a date in time.DateOnly form (YYYY-MM-DD). The error is the one time.Parse
// returns.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, err
	}
	return DateOf(t), nil
}

// String formats d as YYYY-MM-DD (time.DateOnly).
func (d Date) String() string {
	return d.utcMidnight().Format(time.DateOnly)
}

// AddDays returns the date n calendar days after d (before d when n is negative), normalised
// like time.Date: it counts dates, not 24 hour spans.
func (d Date) AddDays(n int) Date {
	return DateOf(time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC))
}

// Compare returns -1 when d is before o, +1 when it is after, and 0 when they are the same
// date.
func (d Date) Compare(o Date) int {
	return d.utcMidnight().Compare(o.utcMidnight())
}

// DaysBetween returns the number of calendar days from from to to, negative when to is before
// from. It subtracts Unix seconds of UTC midnights, so spans longer than the 292 years that
// time.Time.Sub can hold are exact.
func DaysBetween(from, to Date) int {
	return int((to.utcMidnight().Unix() - from.utcMidnight().Unix()) / secondsPerDay)
}

// CalendarDaysBetween returns the number of calendar days from the date of from to the date of
// to, each read in its own location (convert both to the same zone first). It is negative when
// to's date is before from's. It counts dates, not elapsed time, so a span that crosses a DST
// change (a 23 or 25 hour day) still counts each day once.
func CalendarDaysBetween(from, to time.Time) int {
	return DaysBetween(DateOf(from), DateOf(to))
}

// Dates iterates over every date from from to to inclusive, in order. It yields nothing when to
// is before from.
func Dates(from, to Date) iter.Seq[Date] {
	return func(yield func(Date) bool) {
		for d := from; d.Compare(to) <= 0; d = d.AddDays(1) {
			if !yield(d) {
				return
			}
		}
	}
}

// utcMidnight returns 00:00 UTC on d. It is only a carrier for date arithmetic, not an
// instant of the station's clock.
func (d Date) utcMidnight() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}
