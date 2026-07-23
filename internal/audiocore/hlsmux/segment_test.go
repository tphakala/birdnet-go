package hlsmux

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSegmentNameRoundTrip(t *testing.T) {
	t.Parallel()

	for _, seq := range []uint64{0, 1, 9, 10, 999, 1000, 1 << 32, math.MaxUint64} {
		name := SegmentName(seq)
		got, ok := ParseSegmentName(name)
		require.True(t, ok, "%q must parse", name)
		assert.Equal(t, seq, got)
	}
}

func TestParseSegmentNameRejectsJunk(t *testing.T) {
	t.Parallel()

	// The HTTP layer uses this as its only validation of a path element, so
	// anything that is not exactly a name this package emits must be refused.
	bad := []string{
		"", "segment.m4s", "segment", "0.m4s", "init.mp4",
		"segment-1.m4s", "segment+1.m4s", "segment 1.m4s",
		"segment1.ts", "segment1.m4s.m4s", "Segment1.m4s",
		"segment0x10.m4s", "../segment1.m4s", "segment1.m4s/../../etc",
		// One past uint64.
		"segment18446744073709551616.m4s",
	}
	for _, name := range bad {
		_, ok := ParseSegmentName(name)
		assert.False(t, ok, "%q must be rejected", name)
	}
}

func TestSegmentWindowEvictionDropsDataReference(t *testing.T) {
	t.Parallel()

	w := newSegmentWindow(2)
	for i := range uint64(3) {
		w.push(&Segment{Seq: i, Data: []byte{byte(i)}})
	}

	require.Len(t, w.retained(), 2)
	assert.Equal(t, uint64(1), w.mediaSequence())

	_, ok := findSegment(w.retained(), 0)
	assert.False(t, ok, "the evicted segment must be gone")

	// The window holds the two newest segments in order. This is the
	// observable half of eviction; that the evicted payload also becomes
	// collectable follows from push building a slice that simply omits the
	// evicted header, which cannot be asserted directly without a finalizer.
	assert.Equal(t, []byte{1}, w.retained()[0].Data)
	assert.Equal(t, []byte{2}, w.retained()[1].Data)
}

// TestSegmentWindowPushLeavesPublishedSliceAlone pins the invariant the
// lock-free read side rests on. Stream hands the slice retained returns straight
// to playlist renders and segment lookups with no lock held, so if push ever
// went back to shifting headers down in place, a reader walking the previous
// slice would see segments change underneath it: a client could be served one
// segment's bytes under another's sequence number, and no test that only reads
// after writing would notice.
func TestSegmentWindowPushLeavesPublishedSliceAlone(t *testing.T) {
	t.Parallel()

	const capacity = 3
	w := newSegmentWindow(capacity)
	for i := range uint64(capacity) {
		w.push(&Segment{Seq: i, Data: []byte{byte(i)}})
	}

	// What a reader would be holding across the next few cuts.
	published := w.retained()
	before := slices.Clone(published)

	for i := range uint64(capacity * 2) {
		w.push(&Segment{Seq: uint64(capacity) + i, Data: []byte{byte(capacity) + byte(i)}})
	}

	assert.Equal(t, before, published, "a published window must be immutable once handed out")
}

// TestCeilSeconds pins EXT-X-TARGETDURATION to a true ceiling. Rounding to
// nearest would report a 2.4s bound as 2, which RFC 8216 tolerates on a
// rounding reading of section 4.3.3.1 but Apple's mediastreamvalidator treats
// as a segment overrunning its declared target.
func TestCeilSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want int
	}{
		{"zero floors at one", 0, 1},
		{"sub-second floors at one", 300 * time.Millisecond, 1},
		{"exactly two seconds", 2 * time.Second, 2},
		{"just over two rounds up", 2001 * time.Millisecond, 3},
		{"well under three still rounds up", 2400 * time.Millisecond, 3},
		{"exactly ten", 10 * time.Second, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ceilSeconds(tt.d))
		})
	}
}

func TestSegmentWindowNeverGrowsPastCapacity(t *testing.T) {
	t.Parallel()

	w := newSegmentWindow(3)
	assert.Empty(t, w.retained())
	for i := range uint64(5) {
		w.push(&Segment{Seq: i})
	}
	assert.Len(t, w.retained(), 3, "the window never grows past its capacity")
}

// TestNewSegmentWindowRaisesDegenerateCapacity guards the audio goroutine: push
// re-slices the retained segments once the window is full, so a zero capacity
// would treat an empty window as full and panic on the first segment, with
// nothing to recover it.
func TestNewSegmentWindowRaisesDegenerateCapacity(t *testing.T) {
	t.Parallel()

	for _, capacity := range []int{-1, 0} {
		// The constructor is inside the closure too: without the raise, a
		// capacity of -1 panics in make() rather than in push, and a panic out
		// here takes the whole test binary down instead of failing this case.
		var w *segmentWindow
		assert.NotPanics(t, func() {
			w = newSegmentWindow(capacity)
			w.push(&Segment{Seq: 1})
			w.push(&Segment{Seq: 2})
		})
		require.NotNil(t, w)
		assert.Len(t, w.retained(), 1)
	}
}

func TestSampleClockIsExactAcrossManyConversions(t *testing.T) {
	t.Parallel()

	// 1024 samples at 48 kHz is 21.333... ms, which no integer number of
	// nanoseconds represents. Summing many of them is where a lost remainder
	// would show up.
	c := sampleClock{rate: testRate}
	const frames = 100_000
	var total time.Duration
	for range frames {
		total += c.advance(aacFrame)
	}

	want := time.Duration(frames) * aacFrame * time.Second / testRate
	assert.Equal(t, want, total)
}
