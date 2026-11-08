package repository

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

const (
	// wallClockSecondsPerHour turns a second of the day into an hour of day in the SQL builders.
	wallClockSecondsPerHour = 3600
	// wallClockHoursPerDay bounds an hour of day in Go code that consumes the SQL results.
	wallClockHoursPerDay = 24

	// wallClockSecondsPerDay is the modulus that reduces a shifted Unix timestamp to its second
	// of the day.
	wallClockSecondsPerDay = 86400

	// sqlUnixEpochDate is the literal date the MySQL date and year expressions add seconds to.
	sqlUnixEpochDate = "1970-01-01"
	// mysqlDateOnlyFormat is the DATE_FORMAT pattern that writes a date as YYYY-MM-DD.
	mysqlDateOnlyFormat = "%Y-%m-%d"
)

// wallClockDateSQL returns a SQL expression for the wall-clock calendar date (YYYY-MM-DD) of
// column, a Unix timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in
// seconds (an integer literal or the CASE of zoneOffsetSQL). The result is exact for every
// value of column + offset, including those before 1970, and does not depend on the database
// session or OS-local time zone.
//
// SQLite: date(column + offset, 'unixepoch')
// MySQL:  DATE_FORMAT(DATE_ADD('1970-01-01', INTERVAL (column + offset) SECOND), '%Y-%m-%d')
//
// The MySQL form adds the seconds to a literal date, which applies no session time zone
// (DATE(FROM_UNIXTIME(...)) would) and takes floor semantics from the server's calendar.
// DATE_FORMAT keeps the result a string: connections use parseTime=True, which would turn a
// DATE into a time.Time and change every date string the repositories return.
func wallClockDateSQL(column, offsetSQL string, isMySQL bool) string {
	if isMySQL {
		return fmt.Sprintf("DATE_FORMAT(DATE_ADD('%s', INTERVAL (%s + %s) SECOND), '%s')", sqlUnixEpochDate, column, offsetSQL, mysqlDateOnlyFormat)
	}
	return fmt.Sprintf("date(%s + %s, 'unixepoch')", column, offsetSQL)
}

// wallClockHourSQL returns a SQL expression for the wall-clock hour (0-23) of column, a Unix
// timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in seconds. The result
// is in 0-23 for every value of column + offset, including those before 1970 (floor semantics,
// like Go's time package). It uses only integer arithmetic, so it depends on no session
// setting. It has no outer parentheses and is safe as the left side of a comparison or IN.
//
// SQLite: CAST(((((column + offset) % 86400) + 86400) % 86400) / 3600 AS INTEGER)
// MySQL:  ((((column + offset) % 86400) + 86400) % 86400) DIV 3600
//
// The second of the day is reduced first: % takes the sign of the dividend, so adding a day and
// reducing again makes it non-negative before the division. The SQLite form is cast to an
// integer so that a REAL column value or an overflowing sum still yields an integer hour.
func wallClockHourSQL(column, offsetSQL string, isMySQL bool) string {
	secondOfDay := fmt.Sprintf("(((%s + %s) %% %d) + %d) %% %d",
		column, offsetSQL, wallClockSecondsPerDay, wallClockSecondsPerDay, wallClockSecondsPerDay)
	if isMySQL {
		return fmt.Sprintf("(%s) DIV %d", secondOfDay, wallClockSecondsPerHour)
	}
	return fmt.Sprintf("CAST((%s) / %d AS INTEGER)", secondOfDay, wallClockSecondsPerHour)
}

// wallClockYearSQL returns a SQL expression for the wall-clock calendar year of column, a Unix
// timestamp, shifted by offsetSQL, a SQL expression for the UTC offset in seconds. It is exact
// for every value of column + offset, including those before 1970.
//
// SQLite: CAST(strftime('%Y', column + offset, 'unixepoch') AS INTEGER)
// MySQL:  YEAR(DATE_ADD('1970-01-01', INTERVAL (column + offset) SECOND))
func wallClockYearSQL(column, offsetSQL string, isMySQL bool) string {
	if isMySQL {
		return fmt.Sprintf("YEAR(DATE_ADD('%s', INTERVAL (%s + %s) SECOND))", sqlUnixEpochDate, column, offsetSQL)
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
	// An open start stays 0, the Unix epoch; rows before it use the oldest segment's offset, and
	// wallclock.Span clamps the start to the epoch in any case.
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
// The SQL forms are those of wallClockHourSQL.
func (r *detectionRepository) wallClockHourExpr(column string, loc *time.Location, from, to int64) string {
	return wallClockHourSQL(column, spanOffsetSQL(column, loc, from, to), r.isMySQL)
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
