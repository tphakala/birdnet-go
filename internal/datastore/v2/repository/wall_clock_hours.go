package repository

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// wallClockSecondsPerHour and wallClockHoursPerDay turn a shifted Unix timestamp into an
	// hour of day.
	wallClockSecondsPerHour = 3600
	wallClockHoursPerDay    = 24

	// openStartZoneFloor is where offset enumeration starts when a search has no start time: the
	// Unix epoch, which precedes every stored row because detected_at is positive.
	openStartZoneFloor int64 = 0

	// hourFilterFutureMargin is how far past now an explicit end time still gets its own offsets:
	// one leap year.
	hourFilterFutureMargin = daysInLeapYear * 24 * time.Hour

	// daysInLeapYear is the length of the longest calendar year.
	daysInLeapYear = 366

	// Clock fields of the last second of a day, see exclusiveDayEnd.
	lastHourOfDay      = 23
	lastMinuteOfHour   = 59
	lastSecondOfMinute = 59
)

// currentTime returns the repository clock, which is time.Now unless a test injected one.
func (r *detectionRepository) currentTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// hourFilterSpan returns the span of Unix seconds over which the hour offsets are enumerated:
// from the start time (or the Unix epoch) to the end time (or now). An explicit start before the
// epoch and an explicit end more than hourFilterFutureMargin past now are pulled in, so the
// enumeration, and the SQL built from it, stay bounded whatever dates the caller gives. Rows
// beyond the end of the span use the newest offset in it.
func (r *detectionRepository) hourFilterSpan(filters *SearchFilters) (from, to int64) {
	now := r.currentTime().Unix()
	from = openStartZoneFloor
	if filters.StartTime != nil {
		from = max(*filters.StartTime, openStartZoneFloor)
	}
	to = now
	if filters.EndTime != nil {
		to = min(*filters.EndTime, now+int64(hourFilterFutureMargin/time.Second))
	}
	return from, to
}

// wallClockHourExpr returns a SQL expression for the wall-clock hour (0-23) of column, a Unix
// timestamp, in loc (nil means UTC). Each row is shifted by the UTC offset in effect at its own
// instant: the offset changes of loc inside [from, to) become a CASE over the column, see
// zoneOffsetSQL. Rows outside [from, to) are not excluded here; the range predicates do that.
//
// SQLite: CAST((column + offset) / 3600 AS INTEGER) % 24
// MySQL:  ((column + offset) DIV 3600) % 24
//
// MySQL uses DIV, which is integer division whatever div_precision_increment is set to, like
// dateFromUnixExpr does with DIV 86400.
func (r *detectionRepository) wallClockHourExpr(column string, loc *time.Location, from, to int64) string {
	if loc == nil {
		loc = time.UTC
	}
	offset := zoneOffsetSQL(column, ZoneOffsetSegments(loc, from, to), GetTimezoneOffsetAt(loc, time.Unix(from, 0)))
	if r.isMySQL {
		return fmt.Sprintf("((%s + %s) DIV %d) %% %d", column, offset, wallClockSecondsPerHour, wallClockHoursPerDay)
	}
	return fmt.Sprintf("CAST((%s + %s) / %d AS INTEGER) %% %d", column, offset, wallClockSecondsPerHour, wallClockHoursPerDay)
}

// zoneOffsetSQL returns a SQL expression for the UTC offset in seconds that applies to column,
// a Unix timestamp. Adjacent segments with the same offset are merged. With no offset change the
// result is a plain integer literal (fallback when there are no segments), otherwise a CASE
// that tests the newest segment first, so recent rows leave after one or two comparisons; the oldest segment is the ELSE branch and the newest is open upward.
//
// The numbers are written into the SQL text rather than bound, so the hour filter does not add
// bind variables next to the label ID list of buildSearchJoins. Only integers produced by
// strconv reach the SQL.
func zoneOffsetSQL(column string, segments []ZoneOffsetSegment, fallback int) string {
	var distinct []ZoneOffsetSegment
	for _, seg := range segments {
		if n := len(distinct); n > 0 && distinct[n-1].OffsetSeconds == seg.OffsetSeconds {
			continue
		}
		distinct = append(distinct, seg)
	}

	switch len(distinct) {
	case 0:
		return strconv.Itoa(fallback)
	case 1:
		return strconv.Itoa(distinct[0].OffsetSeconds)
	}

	var sb strings.Builder
	sb.WriteString("CASE")
	for i := len(distinct) - 1; i > 0; i-- {
		sb.WriteString(" WHEN ")
		sb.WriteString(column)
		sb.WriteString(" >= ")
		sb.WriteString(strconv.FormatInt(distinct[i].Start, 10))
		sb.WriteString(" THEN ")
		sb.WriteString(strconv.Itoa(distinct[i].OffsetSeconds))
	}
	sb.WriteString(" ELSE ")
	sb.WriteString(strconv.Itoa(distinct[0].OffsetSeconds))
	sb.WriteString(" END")
	return sb.String()
}

// exclusiveDayEnd returns the exclusive end of the given local day as Unix seconds: the next
// local midnight in every zone whose midnight DST does not move. It adds one second to
// 23:59:59 instead of asking time.Date for 00:00 of the next day, because in a zone where DST
// skips midnight time.Date resolves that midnight to 23:00 of the same day.
func exclusiveDayEnd(year int, month time.Month, day int, tz *time.Location) int64 {
	return time.Date(year, month, day, lastHourOfDay, lastMinuteOfHour, lastSecondOfMinute, 0, tz).Add(time.Second).Unix()
}
