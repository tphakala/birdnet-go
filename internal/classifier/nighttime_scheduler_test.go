package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

func TestIsNighttime(t *testing.T) {
	t.Parallel()

	// Helsinki: ~60.17N, 24.94E
	sc := suncalc.NewSunCalc(60.17, 24.94)

	// Use a date with well-defined civil dusk/dawn (equinox-ish)
	// March 20: civil dawn ~05:30, civil dusk ~19:00 (approximate)
	date := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	sunTimes, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)

	tests := []struct {
		name    string
		timeStr string
		want    bool
	}{
		{
			name:    "midnight is nighttime",
			timeStr: "2026-03-20T00:00:00",
			want:    true,
		},
		{
			name:    "noon is daytime",
			timeStr: "2026-03-20T12:00:00",
			want:    false,
		},
		{
			name:    "just before civil dawn is nighttime",
			timeStr: sunTimes.CivilDawn.Add(-1 * time.Minute).Format("2006-01-02T15:04:05"),
			want:    true,
		},
		{
			name:    "just after civil dawn is daytime",
			timeStr: sunTimes.CivilDawn.Add(1 * time.Minute).Format("2006-01-02T15:04:05"),
			want:    false,
		},
		{
			name:    "just before civil dusk is daytime",
			timeStr: sunTimes.CivilDusk.Add(-1 * time.Minute).Format("2006-01-02T15:04:05"),
			want:    false,
		},
		{
			name:    "just after civil dusk is nighttime",
			timeStr: sunTimes.CivilDusk.Add(1 * time.Minute).Format("2006-01-02T15:04:05"),
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts, err := time.ParseInLocation("2006-01-02T15:04:05", tt.timeStr, sunTimes.CivilDawn.Location())
			require.NoError(t, err)
			got := isNighttime(sc, ts)
			assert.Equal(t, tt.want, got, "at %s (civilDawn=%s, civilDusk=%s)",
				tt.timeStr, sunTimes.CivilDawn.Format(time.TimeOnly), sunTimes.CivilDusk.Format(time.TimeOnly))
		})
	}
}

func TestNighttimeScheduler_NilSunCalc(t *testing.T) {
	t.Parallel()

	s := newNighttimeScheduler(nil)
	// Nil suncalc: fail open, bat should be active
	assert.True(t, s.isActive())
}

func TestNighttimeScheduler_DisabledSetting(t *testing.T) {
	t.Parallel()

	sc := suncalc.NewSunCalc(60.17, 24.94)
	s := newNighttimeScheduler(sc)

	// When nighttimeOnly is false, model should always be active
	s.refresh(false)
	assert.True(t, s.isActive())
}
