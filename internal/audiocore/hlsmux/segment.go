package hlsmux

import (
	"strconv"
	"strings"
	"time"
)

const (
	// InitSegmentName is the filename the playlist's EXT-X-MAP points at. It
	// is a name rather than a path because nothing here writes to disk; the
	// HTTP layer maps it back to Stream.InitSegment.
	InitSegmentName = "init.mp4"

	// segmentNamePrefix and segmentNameSuffix bracket a media segment's
	// sequence number. The sequence number is written in full rather than
	// zero padded to a fixed width, because a live stream outlives any fixed
	// width: the FFmpeg path's segment%03d.m4s wraps after 1000 segments,
	// which is a little over half an hour at two seconds each.
	segmentNamePrefix = "segment"
	segmentNameSuffix = ".m4s"
)

// Segment is one fragmented-MP4 media segment together with the timeline facts
// the playlist needs to describe it.
type Segment struct {
	// Seq is the media sequence number, counting from zero for the life of
	// the stream. It names the segment and orders the playlist.
	Seq uint64

	// Data is the complete segment: styp, moof and mdat. It is owned by the
	// stream and must be treated as read only; the ring never writes into a
	// published segment's backing array.
	Data []byte

	// Samples is the per-channel sample count actually contained, summed over
	// the segment's access units. It is the ground truth from which Duration
	// derives, never the nominal target.
	Samples int

	// Duration is the exact playback duration of Samples at the stream's
	// sample rate. Consecutive durations sum without drift; see the carry
	// arithmetic in Stream.
	Duration time.Duration

	// PDT is the wall-clock time of this segment's first sample, for
	// EXT-X-PROGRAM-DATE-TIME.
	PDT time.Time

	// Discontinuity marks a break in the timeline immediately before this
	// segment, which happens when the source stalls and resumes. The playlist
	// emits EXT-X-DISCONTINUITY ahead of it.
	Discontinuity bool

	// DiscontinuitySeq is this segment's own discontinuity sequence number,
	// counting every break up to and including its own. It is not what
	// EXT-X-DISCONTINUITY-SEQUENCE reports directly; see
	// ring.discontinuitySequence, which subtracts this segment's own break
	// because the renderer emits the tag for it separately.
	DiscontinuitySeq uint64
}

// SegmentName returns the playlist filename for a media sequence number.
func SegmentName(seq uint64) string {
	return segmentNamePrefix + strconv.FormatUint(seq, 10) + segmentNameSuffix
}

// ParseSegmentName recovers the media sequence number from a segment filename
// produced by SegmentName. It reports false for anything else, so an HTTP
// handler can reject a bad request without a second validation rule.
func ParseSegmentName(name string) (seq uint64, ok bool) {
	digits, found := strings.CutPrefix(name, segmentNamePrefix)
	if !found {
		return 0, false
	}
	digits, found = strings.CutSuffix(digits, segmentNameSuffix)
	if !found || digits == "" {
		return 0, false
	}
	// Reject a leading '+' or '-', which ParseUint would otherwise accept for
	// '+'.
	if digits[0] < '0' || digits[0] > '9' {
		return 0, false
	}
	seq, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	// Require the canonical spelling, so exactly one name maps to each
	// sequence number. ParseUint alone would accept any number of leading
	// zeros, giving an unbounded family of names for one segment; since this
	// is the only validation applied to the path element, that would let a
	// client mint unlimited distinct cache keys for the same body.
	if SegmentName(seq) != name {
		return 0, false
	}
	return seq, true
}

// ring is the bounded window of media segments a live playlist advertises.
// Pushing past capacity evicts the oldest, which is what makes memory use
// constant no matter how long a stream runs.
//
// ring is not safe for concurrent use; Stream serialises access to it.
type ring struct {
	segments []Segment
	capacity int
}

// newRing returns a ring holding at most capacity segments. A capacity below
// one is raised to one: push indexes the window unconditionally once full, so
// a zero capacity would panic on the first segment, on the audio goroutine,
// with nothing to recover it.
func newRing(capacity int) *ring {
	if capacity < 1 {
		capacity = 1
	}
	return &ring{
		segments: make([]Segment, 0, capacity),
		capacity: capacity,
	}
}

// push appends s, evicting the oldest segment when the window is full.
//
// When full it shifts the retained segments down and overwrites the last slot,
// reusing the backing array. Overwriting drops the evicted segment's reference
// to its Data, so the bytes become collectable; the array itself is only ever
// reused for the Segment headers, never for segment payloads, so a segment
// already handed out by get stays valid for as long as its caller holds it.
func (r *ring) push(s *Segment) {
	if len(r.segments) < r.capacity {
		r.segments = append(r.segments, *s)
		return
	}
	copy(r.segments, r.segments[1:])
	r.segments[len(r.segments)-1] = *s
}

// get returns the retained segment with the given sequence number. It reports
// false once the segment has been evicted, which a client that fell too far
// behind will see as a 404.
func (r *ring) get(seq uint64) (Segment, bool) {
	for i := range r.segments {
		if r.segments[i].Seq == seq {
			return r.segments[i], true
		}
	}
	return Segment{}, false
}

// window returns the retained segments, oldest first. The returned slice
// aliases the ring's storage and is only valid while the caller holds the
// stream lock.
func (r *ring) window() []Segment {
	return r.segments
}

// len is how many segments the window currently holds.
func (r *ring) len() int {
	return len(r.segments)
}

// mediaSequence is the sequence number of the oldest retained segment, which
// is what EXT-X-MEDIA-SEQUENCE reports.
func (r *ring) mediaSequence() uint64 {
	if len(r.segments) == 0 {
		return 0
	}
	return r.segments[0].Seq
}

// discontinuitySequence is the EXT-X-DISCONTINUITY-SEQUENCE value for the
// current window.
//
// RFC 8216 section 6.2.1 defines a segment's discontinuity sequence number as
// this tag's value plus the number of EXT-X-DISCONTINUITY tags preceding that
// segment in the playlist. Segment.DiscontinuitySeq already counts the
// segment's own break, so when the oldest retained segment carries one, the
// renderer also emits its EXT-X-DISCONTINUITY and the client would add it a
// second time. Subtracting it here is what keeps the number a client computes
// for a given segment identical across reloads as the window scrolls, which is
// the entire purpose of the tag.
func (r *ring) discontinuitySequence() uint64 {
	if len(r.segments) == 0 {
		return 0
	}
	seq := r.segments[0].DiscontinuitySeq
	if r.segments[0].Discontinuity {
		// cutSegment increments the counter before assigning it, so a segment
		// carrying a break always has DiscontinuitySeq >= 1 and this cannot
		// underflow. Guarding for zero here would paper over a broken
		// invariant rather than report it.
		seq--
	}
	return seq
}
