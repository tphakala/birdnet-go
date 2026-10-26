package privacy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

func TestLocalDayRange(t *testing.T) {
	t.Parallel()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)

	t.Run("31 days is accepted", func(t *testing.T) {
		t.Parallel()
		_, _, endOut, err := localDayRange("2026-01-01", "2026-01-31", time.UTC)
		require.NoError(t, err)
		assert.Equal(t, "2026-01-31", endOut)
	})

	t.Run("32 days is rejected", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := localDayRange("2026-01-01", "2026-02-01", time.UTC)
		require.ErrorIs(t, err, ErrRangeTooLong)
	})

	t.Run("the cap counts calendar dates across a DST change", func(t *testing.T) {
		t.Parallel()
		// Europe/Helsinki changes on 2026-03-29 (23 hour day) and 2026-10-25 (25 hour day).
		for _, tc := range []struct {
			start, end string
			wantErr    error
		}{
			{"2026-03-10", "2026-04-09", nil},
			{"2026-03-10", "2026-04-10", ErrRangeTooLong},
			{"2026-10-10", "2026-11-09", nil},
			{"2026-10-10", "2026-11-10", ErrRangeTooLong},
		} {
			_, _, _, err := localDayRange(tc.start, tc.end, helsinki)
			if tc.wantErr == nil {
				require.NoError(t, err, "%s..%s", tc.start, tc.end)
				continue
			}
			require.ErrorIs(t, err, tc.wantErr, "%s..%s", tc.start, tc.end)
		}
	})

	t.Run("end before start is rejected", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := localDayRange("2026-01-05", "2026-01-04", time.UTC)
		require.ErrorIs(t, err, ErrInvalidDate)
	})

	t.Run("malformed date is rejected", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := localDayRange("2026-1-05", "", time.UTC)
		require.ErrorIs(t, err, ErrInvalidDate)
	})

	t.Run("years that cannot be marshaled are rejected", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ start, end string }{
			{"9999-12-31", "9999-12-31"},
			{"9999-12-31", ""},
			{"1969-12-31", "1969-12-31"},
			{"0000-01-01", "0000-01-01"},
		} {
			_, _, _, err := localDayRange(tc.start, tc.end, kolkata)
			require.ErrorIs(t, err, ErrInvalidDate, "%s..%s", tc.start, tc.end)
		}
	})

	t.Run("the edges of the supported years are accepted", func(t *testing.T) {
		t.Parallel()
		for _, date := range []string{"1970-01-01", "9998-12-31"} {
			start, end, _, err := localDayRange(date, date, kolkata)
			require.NoError(t, err, date)
			_, err = json.Marshal([]time.Time{start.UTC(), end.UTC()})
			require.NoError(t, err, date)
		}
	})

	t.Run("empty end defaults to 31 dates", func(t *testing.T) {
		t.Parallel()
		start, end, endOut, err := localDayRange("2026-01-01", "", time.UTC)
		require.NoError(t, err)
		assert.Equal(t, "2026-01-31", endOut)
		assert.Equal(t, 31*24*time.Hour, end.Sub(start))
	})

	t.Run("DST day is 23 hours", func(t *testing.T) {
		t.Parallel()
		// Europe/Helsinki springs forward on 2026-03-29.
		start, end, _, err := localDayRange("2026-03-29", "2026-03-29", helsinki)
		require.NoError(t, err)
		assert.Equal(t, 23*time.Hour, end.Sub(start))
	})

	t.Run("start is the local midnight", func(t *testing.T) {
		t.Parallel()
		start, _, _, err := localDayRange("2026-01-02", "2026-01-02", kolkata)
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 1, 1, 18, 30, 0, 0, time.UTC), start.UTC())
	})
}

// TestLocalDayRangeSkippedMidnight pins the day bounds of dates whose midnight does not exist.
func TestLocalDayRangeSkippedMidnight(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)

	t.Run("a day starting in a skipped midnight begins at the transition instant", func(t *testing.T) {
		t.Parallel()
		// America/Havana springs forward at 00:00 on 2026-03-08, so the day begins at 01:00 CDT.
		start, end, _, err := localDayRange("2026-03-08", "2026-03-08", zones["America/Havana"])
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC), start.UTC())
		assert.Equal(t, time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC), end.UTC())
	})

	t.Run("a range ending before a skipped midnight ends at the transition instant", func(t *testing.T) {
		t.Parallel()
		// America/Santiago springs forward at 24:00 on 2026-09-05; 2026-09-06 begins at 01:00.
		start, end, _, err := localDayRange("2026-09-05", "2026-09-05", zones["America/Santiago"])
		require.NoError(t, err)
		assert.Equal(t, time.Date(2026, 9, 5, 4, 0, 0, 0, time.UTC), start.UTC())
		assert.Equal(t, time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC), end.UTC())

		start, _, _, err = localDayRange("2026-09-06", "2026-09-06", zones["America/Santiago"])
		require.NoError(t, err)
		assert.Equal(t, end.UTC(), start.UTC(), "consecutive days tile")
	})
}

func TestLocalDayRangeReportsTheTrueDayCount(t *testing.T) {
	t.Parallel()
	// 1970-01-01 to 9998-12-31 is longer than the 292 years time.Time.Sub can hold.
	_, _, _, err := localDayRange("1970-01-01", "9998-12-31", time.UTC)
	require.ErrorIs(t, err, ErrRangeTooLong)
	assert.ErrorContains(t, err, "2932532 days")
}
