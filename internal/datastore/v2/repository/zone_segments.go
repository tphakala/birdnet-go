package repository

import "time"

// zoneSegmentsInitialCap is the initial capacity of the segment slice: a year crosses at
// most a couple of DST changes.
const zoneSegmentsInitialCap = 4

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
// The result has one segment per offset change in the span, so its size and the cost of one
// query per segment grow with the span; callers must keep [start, end) to a bounded period.
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
		segmentEnd := end
		if _, zoneEnd := at.ZoneBounds(); !zoneEnd.IsZero() && zoneEnd.Unix() < end {
			segmentEnd = zoneEnd.Unix()
		}
		// Defensive: a bound that does not advance would loop forever.
		if segmentEnd <= cur {
			segmentEnd = end
		}

		segments = append(segments, ZoneOffsetSegment{Start: cur, End: segmentEnd, OffsetSeconds: offset})
		cur = segmentEnd
	}
	return segments
}
