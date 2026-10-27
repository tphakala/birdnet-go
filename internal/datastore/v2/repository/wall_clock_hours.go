package repository

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// wallClockSecondsPerHour and wallClockHoursPerDay turn a shifted Unix timestamp into an
	// hour of day.
	wallClockSecondsPerHour = 3600
	wallClockHoursPerDay    = 24

	// wallClockSecondsPerDay turns a shifted Unix timestamp into a day count since the epoch.
	wallClockSecondsPerDay = 86400
)

// currentTime returns the repository clock, which is time.Now unless a test injected one.
func (r *detectionRepository) currentTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// hourFilterSpan returns the span of Unix seconds over which the hour offsets are enumerated:
// from the start time (or the Unix epoch) to the end time (or now), bounded by wallclock.Span.
func (r *detectionRepository) hourFilterSpan(filters *SearchFilters) (from, to int64) {
	now := r.currentTime()
	// An open start stays 0, the Unix epoch, which precedes every stored row because detected_at
	// is positive; wallclock.Span clamps the start to the epoch in any case.
	var start int64
	if filters.StartTime != nil {
		start = *filters.StartTime
	}
	end := now.Unix()
	if filters.EndTime != nil {
		end = *filters.EndTime
	}
	return wallclock.Span(start, end, now)
}

// WallClockDateExpr returns a SQL expression for the wall-clock calendar date (YYYY-MM-DD) of
// column, a Unix timestamp, in loc (nil means UTC). Each row is shifted by the UTC offset in
// effect at its own instant: the offset changes of loc inside [from, to) become a CASE over the
// column, see zoneOffsetSQL. from and to normally come from wallclock.Span. The result does not
// depend on the database session or OS-local time zone.
//
// SQLite: date(column + offset, 'unixepoch')
// MySQL:  DATE_ADD('1970-01-01', INTERVAL (column + offset) DIV 86400 DAY)
//
// The MySQL form adds an integer day count to a literal date, because DATE(FROM_UNIXTIME(...))
// would apply the session time zone on top of the offset. Integer DIV avoids floating-point
// rounding at exact day boundaries.
func WallClockDateExpr(column string, loc *time.Location, from, to int64, isMySQL bool) string {
	if loc == nil {
		loc = time.UTC
	}
	offset := zoneOffsetSQL(column, wallclock.Segments(loc, from, to), wallclock.OffsetAt(loc, time.Unix(from, 0)))
	if isMySQL {
		return fmt.Sprintf("DATE_ADD('1970-01-01', INTERVAL (%s + %s) DIV %d DAY)", column, offset, wallClockSecondsPerDay)
	}
	return fmt.Sprintf("date(%s + %s, 'unixepoch')", column, offset)
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
	offset := zoneOffsetSQL(column, wallclock.Segments(loc, from, to), wallclock.OffsetAt(loc, time.Unix(from, 0)))
	if r.isMySQL {
		return fmt.Sprintf("((%s + %s) DIV %d) %% %d", column, offset, wallClockSecondsPerHour, wallClockHoursPerDay)
	}
	return fmt.Sprintf("CAST((%s + %s) / %d AS INTEGER) %% %d", column, offset, wallClockSecondsPerHour, wallClockHoursPerDay)
}

// zoneOffsetSQL returns a SQL expression for the UTC offset in seconds that applies to column,
// a Unix timestamp. Adjacent segments with the same offset are merged. With no offset change the
// result is a plain integer literal (fallback when there are no segments), otherwise a CASE
// that tests the newest segment first, so recent rows leave after one or two comparisons;
// the oldest segment is the ELSE branch and the newest is open upward.
//
// The numbers are written into the SQL text rather than bound, so the hour filter does not add
// bind variables next to the label ID list of buildSearchJoins. The numbers come from strconv;
// column is written as given.
func zoneOffsetSQL(column string, segments []wallclock.Segment, fallback int) string {
	var distinct []wallclock.Segment
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
