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

	// wallClockSecondsPerDay turns a shifted Unix timestamp into a day count since the epoch,
	// which gives the calendar date and the calendar year.
	wallClockSecondsPerDay = 86400
)

// mysqlHourDivision selects how the MySQL hour expression divides the shifted timestamp by
// the seconds in an hour. Both forms give the same hour while column + offset is not negative.
// Below that, the truncating form returns 0 for the last hour before the shifted epoch and the
// floor form returns -1; SQLite truncates like the first. The two are kept apart so that moving
// the SQL into one builder moves no row between buckets. They are meant to be replaced by one
// floor-modulo form, which removes this choice.
type mysqlHourDivision int

const (
	// mysqlHourDivTruncate writes the hour as ((x DIV 3600) % 24), the form of the hour filter.
	mysqlHourDivTruncate mysqlHourDivision = iota
	// mysqlHourDivFloor writes the hour as FLOOR(x / 3600) % 24, the form of the per-segment
	// analytics queries.
	mysqlHourDivFloor
)

// wallClockDateSQL returns a SQL expression for the wall-clock calendar date (YYYY-MM-DD) of
// column, a Unix timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in
// seconds (an integer literal or the CASE of zoneOffsetSQL). The result does not depend on the
// database session or OS-local time zone. It is exact while column + offset is not negative.
//
// SQLite: date(column + offset, 'unixepoch')
// MySQL:  DATE_ADD('1970-01-01', INTERVAL (column + offset) DIV 86400 DAY)
//
// The MySQL form adds an integer day count to a literal date, because DATE(FROM_UNIXTIME(...))
// would apply the session time zone on top of the offset. Integer DIV avoids floating-point
// rounding at exact day boundaries.
func wallClockDateSQL(column, offsetSQL string, isMySQL bool) string {
	if isMySQL {
		return fmt.Sprintf("DATE_ADD('1970-01-01', INTERVAL (%s + %s) DIV %d DAY)", column, offsetSQL, wallClockSecondsPerDay)
	}
	return fmt.Sprintf("date(%s + %s, 'unixepoch')", column, offsetSQL)
}

// wallClockHourSQL returns a SQL expression for the wall-clock hour (0-23) of column, a Unix
// timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in seconds. The result
// is in 0-23 while column + offset is not negative; div only matters for MySQL below that, see
// mysqlHourDivision.
//
// SQLite: CAST((column + offset) / 3600 AS INTEGER) % 24
// MySQL:  ((column + offset) DIV 3600) % 24  or  FLOOR((column + offset) / 3600) % 24
//
// The truncating MySQL form uses DIV, which is integer division whatever
// div_precision_increment is set to.
func wallClockHourSQL(column, offsetSQL string, isMySQL bool, div mysqlHourDivision) string {
	if !isMySQL {
		return fmt.Sprintf("CAST((%s + %s) / %d AS INTEGER) %% %d", column, offsetSQL, wallClockSecondsPerHour, wallClockHoursPerDay)
	}
	if div == mysqlHourDivFloor {
		return fmt.Sprintf("FLOOR((%s + %s) / %d) %% %d", column, offsetSQL, wallClockSecondsPerHour, wallClockHoursPerDay)
	}
	return fmt.Sprintf("((%s + %s) DIV %d) %% %d", column, offsetSQL, wallClockSecondsPerHour, wallClockHoursPerDay)
}

// wallClockYearSQL returns a SQL expression for the wall-clock calendar year of column, a Unix
// timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in seconds. It is exact
// while column + offset is not negative.
//
// SQLite: CAST(strftime('%Y', column + offset, 'unixepoch') AS INTEGER)
// MySQL:  YEAR(DATE_ADD('1970-01-01', INTERVAL (column + offset) DIV 86400 DAY))
func wallClockYearSQL(column, offsetSQL string, isMySQL bool) string {
	if isMySQL {
		return fmt.Sprintf("YEAR(DATE_ADD('1970-01-01', INTERVAL (%s + %s) DIV %d DAY))", column, offsetSQL, wallClockSecondsPerDay)
	}
	return fmt.Sprintf("CAST(strftime('%%Y', %s + %s, 'unixepoch') AS INTEGER)", column, offsetSQL)
}

// segmentOffsetSQL returns the offset fragment for a query that runs over the single segment
// seg: its UTC offset in seconds as an integer literal.
func segmentOffsetSQL(seg wallclock.Segment) string {
	return strconv.Itoa(seg.OffsetSeconds)
}

// spanOffsetSQL returns the SQL expression for the UTC offset of loc (UTC when nil) at each row
// of column, for rows whose instants lie in [from, to): a CASE over the offset changes of loc
// in the span, or a literal when there are none, see zoneOffsetSQL.
func spanOffsetSQL(column string, loc *time.Location, from, to int64) string {
	if loc == nil {
		loc = time.UTC
	}
	return zoneOffsetSQL(column, wallclock.Segments(loc, from, to), wallclock.OffsetAt(loc, time.Unix(from, 0)))
}

// perRowOffsetSQL returns the SQL expression for the UTC offset of loc (UTC when nil) at each
// row of column, for rows whose instants lie in [start, end): a CASE over the offset changes
// of loc in the span, or a literal when there are none. Rows outside the span use the offset of
// the nearest segment, see wallclock.CoveringSegments.
func perRowOffsetSQL(column string, loc *time.Location, start, end int64, now time.Time) string {
	if loc == nil {
		loc = time.UTC
	}
	return zoneOffsetSQL(column, wallclock.CoveringSegments(loc, start, end, now), wallclock.OffsetAt(loc, time.Unix(start, 0)))
}

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
// depend on the database session or OS-local time zone. The SQL forms are those of
// wallClockDateSQL.
func WallClockDateExpr(column string, loc *time.Location, from, to int64, isMySQL bool) string {
	return wallClockDateSQL(column, spanOffsetSQL(column, loc, from, to), isMySQL)
}

// wallClockHourExpr returns a SQL expression for the wall-clock hour (0-23) of column, a Unix
// timestamp, in loc (nil means UTC). Each row is shifted by the UTC offset in effect at its own
// instant: the offset changes of loc inside [from, to) become a CASE over the column, see
// zoneOffsetSQL. Rows outside [from, to) are not excluded here; the range predicates do that.
// The SQL forms are those of wallClockHourSQL, with the truncating MySQL division.
func (r *detectionRepository) wallClockHourExpr(column string, loc *time.Location, from, to int64) string {
	return wallClockHourSQL(column, spanOffsetSQL(column, loc, from, to), r.isMySQL, mysqlHourDivTruncate)
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
