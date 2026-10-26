package conf

import "time"

// calendarDay is the length of a day on the UTC calendar, which has no DST.
const calendarDay = 24 * time.Hour

// CalendarDaysBetween returns the number of calendar days from the date of from to the
// date of to, each read in its own location (convert both to the same zone first). It is
// negative when to's date is before from's. It counts dates, not elapsed time, so a span
// that crosses a DST change (a 23 or 25 hour day) still counts each day once.
func CalendarDaysBetween(from, to time.Time) int {
	fy, fm, fd := from.Date()
	ty, tm, td := to.Date()
	start := time.Date(fy, fm, fd, 0, 0, 0, 0, time.UTC)
	end := time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC)
	return int(end.Sub(start) / calendarDay)
}
