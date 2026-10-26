package privacy

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
