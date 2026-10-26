package v2only

import (
	"testing"
	"time"
	_ "time/tzdata" // zone data for the named locations, independent of the host

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalHourWindow(t *testing.T) {
	t.Parallel()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)

	// wall returns the instant the Helsinki clock reads the given date and time at.
	// The times used below are unambiguous.
	wall := func(month time.Month, day, hour int) int64 {
		return time.Date(2026, month, day, hour, 0, 0, 0, helsinki).Unix()
	}

	tests := []struct {
		name      string
		date      string
		hour      string
		hours     int
		wantStart int64
		wantEnd   int64
	}{
		{"ordinary hour", "2026-06-10", "09", 1, wall(time.June, 10, 9), wall(time.June, 10, 10)},
		{"multi hour window", "2026-06-10", "09", 3, wall(time.June, 10, 9), wall(time.June, 10, 12)},
		{"last hour of the day ends at the next midnight", "2026-06-10", "23", 1, wall(time.June, 10, 23), wall(time.June, 11, 0)},
		{"window past midnight is clamped to the date", "2026-06-10", "23", 2, wall(time.June, 10, 23), wall(time.June, 11, 0)},
		{"full day from midnight", "2026-06-10", "0", 24, wall(time.June, 10, 0), wall(time.June, 11, 0)},
		{"duration beyond a day is clamped", "2026-06-10", "5", 99, wall(time.June, 10, 5), wall(time.June, 11, 0)},
		{"zero hours is empty", "2026-06-10", "09", 0, wall(time.June, 10, 9), wall(time.June, 10, 9)},
		{"negative hours is empty", "2026-06-10", "09", -3, wall(time.June, 10, 9), wall(time.June, 10, 9)},
		// 2026-10-25 has 25 hours: the clock goes from 04:00 EEST back to 03:00 EET.
		{"fall-back day last hour", "2026-10-25", "23", 1, wall(time.October, 25, 23), wall(time.October, 26, 0)},
		{"fall-back day hour after the change", "2026-10-25", "4", 1, wall(time.October, 25, 4), wall(time.October, 25, 5)},
		// 2026-03-29 has 23 hours: 03:00 does not exist and the window for it is empty.
		{"spring-forward skipped hour is empty", "2026-03-29", "3", 1, wall(time.March, 29, 4), wall(time.March, 29, 4)},
		{"spring-forward day last hour", "2026-03-29", "23", 1, wall(time.March, 29, 23), wall(time.March, 30, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			start, end, err := localHourWindow(tt.date, tt.hour, tt.hours, helsinki)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStart, start, "start")
			assert.Equal(t, tt.wantEnd, end, "end")
			assert.LessOrEqual(t, start, end, "window must never be inverted")
		})
	}

	// 03:00 occurs twice on the fall-back day (EEST, then EET); hour 3 starts at the first
	// occurrence and runs to 04:00 EET, which is two elapsed hours.
	t.Run("repeated hour spans both occurrences", func(t *testing.T) {
		t.Parallel()
		start, end, err := localHourWindow("2026-10-25", "3", 1, helsinki)
		require.NoError(t, err)
		assert.Equal(t, int64(2*time.Hour/time.Second), end-start)
		assert.Equal(t, wall(time.October, 25, 4), end)
	})

	t.Run("date is validated before hour", func(t *testing.T) {
		t.Parallel()
		_, _, err := localHourWindow("not-a-date", "99", 1, helsinki)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrInvalidHour)
	})

	t.Run("invalid hours are rejected", func(t *testing.T) {
		t.Parallel()
		for _, hour := range []string{"24", "-1", "abc", ""} {
			_, _, err := localHourWindow("2026-06-10", hour, 1, helsinki)
			require.ErrorIs(t, err, ErrInvalidHour, "hour %q", hour)
		}
	})
}
