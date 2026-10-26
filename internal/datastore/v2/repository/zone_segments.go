package repository

import "time"

// zoneSegmentsInitialCap is the initial capacity of the segment slice: a year crosses at
// most a couple of DST changes.
const zoneSegmentsInitialCap = 4

// zoneBoundStuckStepSeconds is how far ZoneOffsetSegments moves on when a zone bound does not
// advance past the instant it was asked for. The stuck bound is a year-end instant, and the
// walk resumes one day later, which passes it.
const zoneBoundStuckStepSeconds = 24 * 60 * 60

// ZoneOffsetSegment is a half-open span [Start, End) of Unix seconds over which a
// location's UTC offset is constant.
type ZoneOffsetSegment struct {
	// Start and End are Unix seconds; the span includes Start and excludes End.
	Start, End int64
	// OffsetSeconds is the location's UTC offset (seconds east of UTC) throughout the span.
	OffsetSeconds int
}

// ZoneOffsetSegments splits [start, end) at the UTC offset changes of loc (time.Local when
// nil), so SQL that adds a fixed offset to detected_at stays exact within each segment.
//
// The segments are contiguous, do not overlap, and together cover [start, end) exactly once.
// A range with no offset change yields a single segment, and end <= start yields none.
// Neighbouring pieces with the same offset are merged, so there is one segment per run of
// constant offset (one more than the number of offset changes inside the span). Its size and
// the cost of one query per segment grow with the span; callers must keep [start, end) to a
// bounded period.
// Callers that run one query per segment can merge the results without double counting, as
// long as every row falls in exactly one segment.
func ZoneOffsetSegments(loc *time.Location, start, end int64) []ZoneOffsetSegment {
	if loc == nil {
		loc = time.Local
	}
	segments := make([]ZoneOffsetSegment, 0, zoneSegmentsInitialCap)
	for cur := start; cur < end; {
		at := time.Unix(cur, 0).In(loc)
		_, offset := at.Zone()

		// ZoneBounds reports when the current zone period ends; a zero end means it never does.
		next := end
		if _, zoneEnd := at.ZoneBounds(); !zoneEnd.IsZero() {
			if bound := zoneEnd.Unix(); bound > cur {
				next = min(bound, end)
			} else {
				// For zones resolved from their POSIX rule, Go reports the end of the year as
				// the bound, and on 31 December of a leap year that bound is the instant
				// itself. It is not a real offset change, so step past it by a day and keep
				// walking instead of treating the rest of the span as one period.
				next = min(cur+zoneBoundStuckStepSeconds, end)
			}
		}

		// Neighbouring pieces with the same offset (a year-end bound, or a change of
		// abbreviation only) form one segment.
		if last := len(segments) - 1; last >= 0 && segments[last].OffsetSeconds == offset {
			segments[last].End = next
		} else {
			segments = append(segments, ZoneOffsetSegment{Start: cur, End: next, OffsetSeconds: offset})
		}
		cur = next
	}
	return segments
}
