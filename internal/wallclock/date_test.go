package wallclock_test

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

func date(year int, month time.Month, day int) wallclock.Date {
	return wallclock.Date{Year: year, Month: month, Day: day}
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	got, err := wallclock.ParseDate("2026-02-28")
	require.NoError(t, err)
	assert.Equal(t, date(2026, time.February, 28), got)
	assert.Equal(t, "2026-02-28", got.String())

	for _, bad := range []string{"", "2026-2-3", "2026-02-30", "26-02-03", "2026-02-03T00:00:00Z", "not a date"} {
		_, err := wallclock.ParseDate(bad)
		assert.Error(t, err, "input %q", bad)
	}
}

func TestDateOfReadsTheTimesOwnLocation(t *testing.T) {
	t.Parallel()
	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]
	// 22:30 UTC on the 8th is 01:30 on the 9th in Helsinki (EEST).
	instant := time.Date(2026, time.July, 8, 22, 30, 0, 0, time.UTC)
	assert.Equal(t, date(2026, time.July, 8), wallclock.DateOf(instant))
	assert.Equal(t, date(2026, time.July, 9), wallclock.DateOf(instant.In(helsinki)))
}

func TestDateAddDays(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from wallclock.Date
		n    int
		want wallclock.Date
	}{
		{"within a month", date(2026, time.March, 10), 5, date(2026, time.March, 15)},
		{"across a month end", date(2026, time.January, 30), 3, date(2026, time.February, 2)},
		{"across a year end", date(2026, time.December, 31), 1, date(2027, time.January, 1)},
		{"29 February of a leap year", date(2028, time.February, 28), 1, date(2028, time.February, 29)},
		{"past 29 February", date(2028, time.February, 29), 1, date(2028, time.March, 1)},
		{"no 29 February in a common year", date(2026, time.February, 28), 1, date(2026, time.March, 1)},
		{"backwards across a year start", date(2026, time.January, 1), -1, date(2025, time.December, 31)},
		{"zero", date(2026, time.June, 1), 0, date(2026, time.June, 1)},
		{"a full year of days", date(2026, time.January, 1), 365, date(2027, time.January, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.from.AddDays(tt.n))
		})
	}
}

func TestDateCompare(t *testing.T) {
	t.Parallel()
	a, b := date(2026, time.March, 9), date(2026, time.March, 10)
	assert.Equal(t, -1, a.Compare(b))
	assert.Equal(t, 1, b.Compare(a))
	assert.Equal(t, 0, a.Compare(date(2026, time.March, 9)))
	assert.Equal(t, -1, date(2025, time.December, 31).Compare(date(2026, time.January, 1)))
}

func TestDaysBetween(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		from, to wallclock.Date
		want     int
	}{
		{"same date", date(2026, time.March, 1), date(2026, time.March, 1), 0},
		{"consecutive dates", date(2026, time.March, 1), date(2026, time.March, 2), 1},
		{"reversed is negative", date(2026, time.March, 2), date(2026, time.March, 1), -1},
		{"across a leap day", date(2028, time.February, 28), date(2028, time.March, 1), 2},
		// time.Time.Sub saturates at about 292 years; a day count must not.
		{"span longer than the Duration range", date(1700, time.January, 1), date(2026, time.January, 1), 119069},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, wallclock.DaysBetween(tt.from, tt.to))
		})
	}
}

func TestCalendarDaysBetween(t *testing.T) {
	t.Parallel()

	zones := testutil.ToolchainZones(t)
	helsinki, sydney, kolkata := zones["Europe/Helsinki"], zones["Australia/Sydney"], zones["Asia/Kolkata"]

	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want int
	}{
		{"spring-forward in span", time.Date(2026, 3, 28, 12, 0, 0, 0, helsinki), time.Date(2026, 3, 30, 9, 0, 0, 0, helsinki), 2},
		{"dates not elapsed time", time.Date(2026, 3, 15, 23, 50, 0, 0, helsinki), time.Date(2026, 4, 1, 0, 10, 0, 0, helsinki), 17},
		{"fall-back in span", time.Date(2026, 10, 24, 23, 50, 0, 0, helsinki), time.Date(2026, 10, 26, 0, 10, 0, 0, helsinki), 2},
		{"southern spring-forward", time.Date(2026, 10, 3, 12, 0, 0, 0, sydney), time.Date(2026, 10, 5, 9, 0, 0, 0, sydney), 2},
		{"no DST zone", time.Date(2026, 3, 28, 0, 0, 0, 0, kolkata), time.Date(2026, 3, 30, 0, 0, 0, 0, kolkata), 2},
		{"span longer than the Duration range", time.Date(1700, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 119069},
		{"reversed order is negative", time.Date(2026, 3, 30, 9, 0, 0, 0, helsinki), time.Date(2026, 3, 28, 12, 0, 0, 0, helsinki), -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, wallclock.CalendarDaysBetween(tt.from, tt.to))
		})
	}
}

func TestDates(t *testing.T) {
	t.Parallel()

	t.Run("inclusive range across a month end", func(t *testing.T) {
		t.Parallel()
		got := slices.Collect(wallclock.Dates(date(2026, time.February, 27), date(2026, time.March, 2)))
		assert.Equal(t, []wallclock.Date{
			date(2026, time.February, 27), date(2026, time.February, 28),
			date(2026, time.March, 1), date(2026, time.March, 2),
		}, got)
	})
	t.Run("single day", func(t *testing.T) {
		t.Parallel()
		got := slices.Collect(wallclock.Dates(date(2026, time.March, 2), date(2026, time.March, 2)))
		assert.Equal(t, []wallclock.Date{date(2026, time.March, 2)}, got)
	})
	t.Run("empty when to is before from", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, slices.Collect(wallclock.Dates(date(2026, time.March, 3), date(2026, time.March, 2))))
	})
	t.Run("stops early when the consumer breaks", func(t *testing.T) {
		t.Parallel()
		n := 0
		for range wallclock.Dates(date(2026, time.January, 1), date(2030, time.January, 1)) {
			n++
			if n == 3 {
				break
			}
		}
		assert.Equal(t, 3, n)
	})
}
