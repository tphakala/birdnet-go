package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestZoneOffsetSegments_NilLocationIsLocal swaps time.Local, so it must not run in parallel.
func TestZoneOffsetSegments_NilLocationIsLocal(t *testing.T) {
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	previous := time.Local
	time.Local = helsinki
	t.Cleanup(func() { time.Local = previous })

	start := time.Date(2026, time.March, 20, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC).Unix()
	got := ZoneOffsetSegments(nil, start, end)
	assert.Len(t, got, 3, "a nil location uses time.Local, which crosses both Helsinki changes")
	assert.Equal(t, ZoneOffsetSegments(helsinki, start, end), got)
}

func TestZoneOffsetSegments(t *testing.T) {
	t.Parallel()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)

	const (
		offsetEET  = 2 * 3600
		offsetEEST = 3 * 3600
		offsetIST  = 5*3600 + 30*60
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
		want  []ZoneOffsetSegment
	}{
		{
			name:  "range crossing both Helsinki changes",
			loc:   helsinki,
			start: startMarch,
			end:   endNov,
			want: []ZoneOffsetSegment{
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
			want: []ZoneOffsetSegment{
				{Start: springForward, End: fallBack, OffsetSeconds: offsetEEST},
			},
		},
		{
			name:  "no-DST zone is one segment",
			loc:   kolkata,
			start: startMarch,
			end:   endNov,
			want: []ZoneOffsetSegment{
				{Start: startMarch, End: endNov, OffsetSeconds: offsetIST},
			},
		},
		{
			name:  "start equals end is empty",
			loc:   helsinki,
			start: startMarch,
			end:   startMarch,
			want:  []ZoneOffsetSegment{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ZoneOffsetSegments(tt.loc, tt.start, tt.end))
		})
	}
}
