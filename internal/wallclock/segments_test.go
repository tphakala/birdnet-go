package wallclock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// TestSegmentsNilLocationIsLocal swaps time.Local, so it must not run in parallel.
func TestSegmentsNilLocationIsLocal(t *testing.T) {
	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]
	previous := time.Local
	time.Local = helsinki
	t.Cleanup(func() { time.Local = previous })

	start := time.Date(2026, time.March, 20, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC).Unix()
	got := wallclock.Segments(nil, start, end)
	assert.Len(t, got, 3, "a nil location uses time.Local, which crosses both Helsinki changes")
	assert.Equal(t, wallclock.Segments(helsinki, start, end), got)
	assert.Equal(t, 3*secondsPerHour, wallclock.OffsetAt(nil, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)))
}

// TestSegmentsRuleOnlyZoneAcrossLeapYearEnd pins that the walk continues past the bound that Go
// reports on 31 December of a leap year for zones resolved from their POSIX rule: that bound does
// not move forward, and every later offset change must still be found.
func TestSegmentsRuleOnlyZoneAcrossLeapYearEnd(t *testing.T) {
	t.Parallel()

	loc := testutil.RuleOnlyLocation(t)

	const (
		offsetEET  = 2 * secondsPerHour
		offsetEEST = 3 * secondsPerHour
	)
	utc := func(y int, m time.Month, d int) int64 {
		return time.Date(y, m, d, 1, 0, 0, 0, time.UTC).Unix()
	}
	start := time.Date(2024, time.June, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()

	changes := []int64{
		utc(2024, time.October, 27),
		utc(2025, time.March, 30),
		utc(2025, time.October, 26),
		utc(2026, time.March, 29),
		utc(2026, time.October, 25),
	}
	want := []wallclock.Segment{
		{Start: start, End: changes[0], OffsetSeconds: offsetEEST},
		{Start: changes[0], End: changes[1], OffsetSeconds: offsetEET},
		{Start: changes[1], End: changes[2], OffsetSeconds: offsetEEST},
		{Start: changes[2], End: changes[3], OffsetSeconds: offsetEET},
		{Start: changes[3], End: changes[4], OffsetSeconds: offsetEEST},
		{Start: changes[4], End: end, OffsetSeconds: offsetEET},
	}
	assert.Equal(t, want, wallclock.Segments(loc, start, end))
}

func TestSegments(t *testing.T) {
	t.Parallel()

	zones := testutil.ToolchainZones(t)
	helsinki, kolkata := zones["Europe/Helsinki"], zones["Asia/Kolkata"]

	const (
		offsetEET  = 2 * secondsPerHour
		offsetEEST = 3 * secondsPerHour
		offsetIST  = 5*secondsPerHour + 30*secondsPerMinute
	)
	springForward := time.Date(2026, time.March, 29, 1, 0, 0, 0, time.UTC).Unix()
	fallBack := time.Date(2026, time.October, 25, 1, 0, 0, 0, time.UTC).Unix()
	startMarch := time.Date(2026, time.March, 20, 0, 0, 0, 0, helsinki).Unix()
	endNov := time.Date(2026, time.November, 1, 0, 0, 0, 0, helsinki).Unix()

	tests := []struct {
		name  string
		loc   *time.Location
		start int64
		end   int64
		want  []wallclock.Segment
	}{
		{
			name:  "range crossing both Helsinki changes",
			loc:   helsinki,
			start: startMarch,
			end:   endNov,
			want: []wallclock.Segment{
				{Start: startMarch, End: springForward, OffsetSeconds: offsetEET},
				{Start: springForward, End: fallBack, OffsetSeconds: offsetEEST},
				{Start: fallBack, End: endNov, OffsetSeconds: offsetEET},
			},
		},
		{
			name:  "end exactly at a change emits no empty trailing segment",
			loc:   helsinki,
			start: springForward,
			end:   fallBack,
			want:  []wallclock.Segment{{Start: springForward, End: fallBack, OffsetSeconds: offsetEEST}},
		},
		{
			name:  "no-DST zone is one segment",
			loc:   kolkata,
			start: startMarch,
			end:   endNov,
			want:  []wallclock.Segment{{Start: startMarch, End: endNov, OffsetSeconds: offsetIST}},
		},
		{
			name:  "start equals end is empty",
			loc:   helsinki,
			start: startMarch,
			end:   startMarch,
			want:  []wallclock.Segment{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, wallclock.Segments(tt.loc, tt.start, tt.end))
		})
	}
}

func TestOffsetAt(t *testing.T) {
	t.Parallel()
	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]
	assert.Equal(t, 2*secondsPerHour, wallclock.OffsetAt(helsinki, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)))
	assert.Equal(t, 3*secondsPerHour, wallclock.OffsetAt(helsinki, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)))
}

func TestCoveringSegments(t *testing.T) {
	t.Parallel()

	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)

	t.Run("start at or after end is nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, wallclock.CoveringSegments(helsinki, 100, 100, now))
		assert.Nil(t, wallclock.CoveringSegments(helsinki, 200, 100, now))
	})

	t.Run("first and last segment are stretched to the requested range", func(t *testing.T) {
		t.Parallel()
		start := time.Date(1960, time.January, 1, 0, 0, 0, 0, time.UTC).Unix()
		end := now.Unix() + 5*366*secondsPerDay
		got := wallclock.CoveringSegments(helsinki, start, end, now)
		require.Greater(t, len(got), 2)
		assert.Equal(t, start, got[0].Start, "rows before the epoch use the nearest segment's offset")
		assert.Equal(t, end, got[len(got)-1].End, "rows past the bounded span use the newest offset")
		for i := 1; i < len(got); i++ {
			assert.Equal(t, got[i-1].End, got[i].Start, "segments tile the range")
		}
	})

	t.Run("range starting past the bounded span gets one segment", func(t *testing.T) {
		t.Parallel()
		start := now.Unix() + 3*366*secondsPerDay
		end := start + 30*secondsPerDay
		got := wallclock.CoveringSegments(helsinki, start, end, now)
		// The span ends one leap year past now, so no change is enumerated; the segment carries
		// the offset in effect at the span's start (from, which is start).
		want := []wallclock.Segment{{
			Start:         start,
			End:           end,
			OffsetSeconds: wallclock.OffsetAt(helsinki, time.Unix(start, 0)),
		}}
		assert.Equal(t, want, got)
	})
}
