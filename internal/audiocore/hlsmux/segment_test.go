package hlsmux

import (
	"math"
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

func TestRingEvictionDropsDataReference(t *testing.T) {
	t.Parallel()

	r := newRing(2)
	for i := range uint64(3) {
		r.push(&Segment{Seq: i, Data: []byte{byte(i)}})
	}

	require.Len(t, r.window(), 2)
	assert.Equal(t, uint64(1), r.mediaSequence())

	_, ok := r.get(0)
	assert.False(t, ok, "the evicted segment must be gone")

	// The window holds the two newest segments in order. This is the
	// observable half of eviction; that the evicted payload also becomes
	// collectable follows from push overwriting the slot rather than
	// re-slicing, which cannot be asserted directly without a finalizer.
	assert.Equal(t, []byte{1}, r.window()[0].Data)
	assert.Equal(t, []byte{2}, r.window()[1].Data)
}

func TestRingTargetDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		durations []time.Duration
		want      int
	}{
		{"empty ring floors at one", nil, 1},
		{"sub-second segments floor at one", []time.Duration{300 * time.Millisecond}, 1},
		{"two second segments", []time.Duration{2005 * time.Millisecond, 1994 * time.Millisecond}, 2},
		{"rounds to nearest, not up", []time.Duration{2400 * time.Millisecond}, 2},
		{"rounds up past the halfway point", []time.Duration{2600 * time.Millisecond}, 3},
		{"takes the maximum", []time.Duration{time.Second, 4 * time.Second, 2 * time.Second}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRing(len(tt.durations) + 1)
			for i, d := range tt.durations {
				r.push(&Segment{Seq: uint64(i), Duration: d})
			}
			assert.Equal(t, tt.want, r.targetDuration())
		})
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
