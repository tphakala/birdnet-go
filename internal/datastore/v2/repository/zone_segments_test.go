package repository

import (
	"bytes"
	"encoding/binary"
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

// ruleOnlyZoneRule is the POSIX rule of the rule-only test zone (EET with EEST in summer).
const ruleOnlyZoneRule = "EET-2EEST,M3.5.0/3,M10.5.0/4"

// ruleOnlyTZif builds a minimal version 2 TZif file whose single transition lies at the epoch,
// so every later instant is resolved from the POSIX rule in the footer, as it is for the
// years after the last transition of a slim zone file.
func ruleOnlyTZif(t *testing.T) []byte {
	t.Helper()

	const (
		utcOffsetSeconds = 2 * 3600
		abbreviations    = "EET\x00"
	)
	writeHeader := func(w *bytes.Buffer) {
		w.WriteString("TZif2")
		w.Write(make([]byte, 15))
		// isutcnt, isstdcnt, leapcnt, timecnt, typecnt, charcnt
		for _, n := range []uint32{0, 0, 0, 1, 1, uint32(len(abbreviations))} {
			require.NoError(t, binary.Write(w, binary.BigEndian, n))
		}
	}
	writeBody := func(w *bytes.Buffer, transitionTime any) {
		require.NoError(t, binary.Write(w, binary.BigEndian, transitionTime))
		w.WriteByte(0) // transition type index
		require.NoError(t, binary.Write(w, binary.BigEndian, int32(utcOffsetSeconds)))
		w.WriteByte(0) // not DST
		w.WriteByte(0) // abbreviation index
		w.WriteString(abbreviations)
	}

	var w bytes.Buffer
	writeHeader(&w)
	writeBody(&w, int32(0))
	writeHeader(&w)
	writeBody(&w, int64(0))
	w.WriteString("\n" + ruleOnlyZoneRule + "\n")
	return w.Bytes()
}

// TestZoneOffsetSegments_RuleOnlyZoneAcrossLeapYearEnd pins that the walk continues past the
// bound that Go reports on 31 December of a leap year for zones resolved from their POSIX
// rule: that bound does not move forward, and every later offset change must still be found.
func TestZoneOffsetSegments_RuleOnlyZoneAcrossLeapYearEnd(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocationFromTZData("Test/RuleOnly", ruleOnlyTZif(t))
	require.NoError(t, err)

	const (
		offsetEET  = 2 * 3600
		offsetEEST = 3 * 3600
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
	want := []ZoneOffsetSegment{
		{Start: start, End: changes[0], OffsetSeconds: offsetEEST},
		{Start: changes[0], End: changes[1], OffsetSeconds: offsetEET},
		{Start: changes[1], End: changes[2], OffsetSeconds: offsetEEST},
		{Start: changes[2], End: changes[3], OffsetSeconds: offsetEET},
		{Start: changes[3], End: changes[4], OffsetSeconds: offsetEEST},
		{Start: changes[4], End: end, OffsetSeconds: offsetEET},
	}
	assert.Equal(t, want, ZoneOffsetSegments(loc, start, end))
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
