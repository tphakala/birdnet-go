package wallclock

import "time"

const (
	// segmentsInitialCap is the initial capacity of the segment slice: a year crosses at most a
	// couple of DST changes.
	segmentsInitialCap = 4

	// zoneBoundStuckStepSeconds is how far Segments moves on when a zone bound does not advance
	// past the instant it was asked for. For zones resolved from their POSIX rule Go computes
	// that bound as 365 days after the start of the year, which is 31 December 00:00 in a leap
	// year, so every instant on that day sees a bound at or before itself. Resuming one day
	// later lands on or after 1 January, where the bound advances again.
	zoneBoundStuckStepSeconds = secondsPerDay

	// spanEpochFloor is where offset enumeration starts at the earliest: the Unix epoch, which
	// precedes every stored row because detected_at is positive.
	spanEpochFloor int64 = 0

	// spanFutureMargin is how far past now an explicit end still gets its own offsets: one leap
	// year.
	spanFutureMargin = daysInLeapYear * secondsPerDay

	// daysInLeapYear is the length of the longest calendar year.
	daysInLeapYear = 366
)

// Segment is a half-open span [Start, End) of Unix seconds over which a location's UTC offset
// is constant.
type Segment struct {
	// Start and End are Unix seconds; the span includes Start and excludes End.
	Start, End int64
	// OffsetSeconds is the location's UTC offset (seconds east of UTC) throughout the span.
	OffsetSeconds int
}

// OffsetAt returns the UTC offset of loc (time.Local when nil) in seconds east of UTC at the
// instant t, DST included.
//
// LIMITATION: a single offset applied to a whole query is wrong for rows recorded in a
// different DST state than t. When bucketing data that spans a DST transition, split the range
// with Segments or CoveringSegments, or apply the offset in effect at each row.
func OffsetAt(loc *time.Location, t time.Time) int {
	if loc == nil {
		loc = time.Local
	}
	_, offset := t.In(loc).Zone()
	return offset
}

// Segments splits [start, end) at the UTC offset changes of loc (time.Local when nil), so SQL
// that adds a fixed offset to a Unix timestamp stays exact within each segment.
//
// The segments are contiguous, do not overlap, and together cover [start, end) exactly once. A
// range with no offset change yields a single segment, and end <= start yields none.
// Neighbouring pieces with the same offset are merged, so there is one segment per run of
// constant offset (one more than the number of offset changes inside the span). Its size and
// the cost of one query per segment grow with the span; callers must keep [start, end) to a
// bounded period.
// Callers that run one query per segment can merge the results without double counting, as long
// as every row falls in exactly one segment.
func Segments(loc *time.Location, start, end int64) []Segment {
	if loc == nil {
		loc = time.Local
	}
	segments := make([]Segment, 0, segmentsInitialCap)
	for cur := start; cur < end; {
		at := time.Unix(cur, 0).In(loc)
		_, offset := at.Zone()

		// ZoneBounds reports when the current zone period ends; a zero end means it never does.
		next := end
		if _, zoneEnd := at.ZoneBounds(); !zoneEnd.IsZero() {
			if bound := zoneEnd.Unix(); bound > cur {
				next = min(bound, end)
			} else {
				// The bound is at or before cur (see zoneBoundStuckStepSeconds). It is not a
				// real offset change, so step a day and keep walking instead of treating the
				// rest of the span as one period.
				next = min(cur+zoneBoundStuckStepSeconds, end)
			}
		}

		// Neighbouring pieces with the same offset (a year-end bound, or a change of
		// abbreviation only) form one segment.
		if last := len(segments) - 1; last >= 0 && segments[last].OffsetSeconds == offset {
			segments[last].End = next
		} else {
			segments = append(segments, Segment{Start: cur, End: next, OffsetSeconds: offset})
		}
		cur = next
	}
	return segments
}

// Span returns the span of Unix seconds over which a zone's offset changes are enumerated for a
// query over [start, end): start pulled up to the Unix epoch and end pulled in to now plus one
// leap year, so the enumeration, and the SQL built from it, stay bounded whatever dates the
// caller gives. Rows beyond the end of the span use the newest offset in it.
func Span(start, end int64, now time.Time) (from, to int64) {
	from = max(start, spanEpochFloor)
	to = min(end, now.Unix()+spanFutureMargin)
	return from, to
}

// CoveringSegments splits [start, end) at the offset changes of loc, like Segments, over the
// bounded span of Span: the enumeration starts at the Unix epoch at the earliest and ends one
// leap year past now at the latest. The first and last segment are then stretched back to start
// and end, so rows outside the bounded span use the offset of the nearest segment. A range that
// starts past the bound has no enumerated segment and gets one segment with the offset in
// effect at the start of the span. It returns nil when start >= end. A range with an open start
// costs one query per offset period since 1970 in callers that query per segment.
func CoveringSegments(loc *time.Location, start, end int64, now time.Time) []Segment {
	if start >= end {
		return nil
	}
	from, to := Span(start, end, now)
	segments := Segments(loc, from, to)
	if len(segments) == 0 {
		// The range starts at or past the bound, so no offset change is enumerated.
		return []Segment{{
			Start:         start,
			End:           end,
			OffsetSeconds: OffsetAt(loc, time.Unix(from, 0)),
		}}
	}
	segments[0].Start = start
	segments[len(segments)-1].End = end
	return segments
}
