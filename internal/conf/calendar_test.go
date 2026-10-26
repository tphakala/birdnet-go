package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalendarDaysBetween(t *testing.T) {
	t.Parallel()

	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	sydney, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)

	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want int
	}{
		{"spring-forward in span", time.Date(2026, 3, 28, 12, 0, 0, 0, helsinki), time.Date(2026, 3, 30, 9, 0, 0, 0, helsinki), 2},
		{"dates not elapsed time", time.Date(2026, 3, 15, 23, 50, 0, 0, helsinki), time.Date(2026, 4, 1, 0, 10, 0, 0, helsinki), 17},
		{"fall-back in span", time.Date(2026, 10, 24, 12, 0, 0, 0, helsinki), time.Date(2026, 10, 26, 9, 0, 0, 0, helsinki), 2},
		{"southern spring-forward", time.Date(2026, 10, 3, 12, 0, 0, 0, sydney), time.Date(2026, 10, 5, 9, 0, 0, 0, sydney), 2},
		{"no DST zone", time.Date(2026, 3, 28, 0, 0, 0, 0, kolkata), time.Date(2026, 3, 30, 0, 0, 0, 0, kolkata), 2},
		{"span longer than the Duration range", time.Date(1700, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 119069},
		{"reversed order is negative", time.Date(2026, 3, 30, 9, 0, 0, 0, helsinki), time.Date(2026, 3, 28, 12, 0, 0, 0, helsinki), -2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CalendarDaysBetween(tt.from, tt.to))
		})
	}
}
