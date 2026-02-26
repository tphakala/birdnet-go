package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

func TestIsTimeInWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		now      string // HH:MM
		start    string // HH:MM
		end      string // HH:MM
		expected bool
	}{
		// Same-day windows
		{"in daytime window", "14:00", "08:00", "18:00", true},
		{"before daytime window", "06:00", "08:00", "18:00", false},
		{"after daytime window", "20:00", "08:00", "18:00", false},
		{"at start of daytime window", "08:00", "08:00", "18:00", true},
		{"at end of daytime window", "18:00", "08:00", "18:00", false},

		// Overnight windows (start > end)
		{"in overnight window late", "23:00", "22:00", "06:00", true},
		{"in overnight window early", "03:00", "22:00", "06:00", true},
		{"outside overnight window", "12:00", "22:00", "06:00", false},
		{"at start of overnight window", "22:00", "22:00", "06:00", true},
		{"at end of overnight window", "06:00", "22:00", "06:00", false},
		{"just before end of overnight window", "05:59", "22:00", "06:00", true},

		// Edge cases
		{"midnight in overnight window", "00:00", "22:00", "06:00", true},
		{"noon not in overnight window", "12:00", "22:00", "06:00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			now := makeTime(tt.now)
			start := makeTime(tt.start)
			end := makeTime(tt.end)

			result := isTimeInWindow(now, start, end)
			assert.Equal(t, tt.expected, result,
				"isTimeInWindow(%s, %s, %s)", tt.now, tt.start, tt.end)
		})
	}
}

func TestParseHHMM(t *testing.T) {
	t.Parallel()

	ref := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)

	tests := []struct {
		name     string
		input    string
		wantHour int
		wantMin  int
	}{
		{"midnight", "00:00", 0, 0},
		{"morning", "06:30", 6, 30},
		{"afternoon", "14:45", 14, 45},
		{"late night", "23:59", 23, 59},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := parseHHMM(tt.input, ref)
			assert.Equal(t, tt.wantHour, result.Hour(), "hour mismatch for %q", tt.input)
			assert.Equal(t, tt.wantMin, result.Minute(), "minute mismatch for %q", tt.input)
			assert.Equal(t, ref.Year(), result.Year(), "year mismatch for %q", tt.input)
			assert.Equal(t, ref.Month(), result.Month(), "month mismatch for %q", tt.input)
			assert.Equal(t, ref.Day(), result.Day(), "day mismatch for %q", tt.input)
		})
	}
}

func TestIsInQuietHours_Disabled(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled: false,
	}

	assert.False(t, s.isInQuietHours(qh, time.Now()), "disabled quiet hours should return false")
}

func TestIsInQuietHours_FixedMode(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:   true,
		Mode:      "fixed",
		StartTime: "22:00",
		EndTime:   "06:00",
	}

	// 23:00 should be in quiet hours (overnight window)
	at2300 := time.Date(2025, 6, 15, 23, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(qh, at2300), "23:00 should be in quiet hours (22:00-06:00)")

	// 12:00 should NOT be in quiet hours
	at1200 := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	assert.False(t, s.isInQuietHours(qh, at1200), "12:00 should NOT be in quiet hours (22:00-06:00)")

	// 03:00 should be in quiet hours (early morning in overnight window)
	at0300 := time.Date(2025, 6, 15, 3, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(qh, at0300), "03:00 should be in quiet hours (22:00-06:00)")
}

func TestIsInQuietHours_SolarMode(t *testing.T) {
	t.Parallel()

	// Create a SunCalc for a known location (roughly central US)
	sc := suncalc.NewSunCalc(40.0, -90.0)

	s := &QuietHoursScheduler{
		sunCalc:    sc,
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:     true,
		Mode:        "solar",
		StartEvent:  "sunset",
		StartOffset: 30, // 30 minutes after sunset
		EndEvent:    "sunrise",
		EndOffset:   -30, // 30 minutes before sunrise
	}

	// Test at midnight (should be in quiet hours - between sunset+30 and sunrise-30)
	midnight := time.Date(2025, 6, 15, 0, 0, 0, 0, time.Local)
	assert.True(t, s.isInQuietHours(qh, midnight),
		"midnight should be in quiet hours (after sunset, before sunrise)")

	// Test at noon (should NOT be in quiet hours)
	noon := time.Date(2025, 6, 15, 12, 0, 0, 0, time.Local)
	assert.False(t, s.isInQuietHours(qh, noon),
		"noon should NOT be in quiet hours")
}

func TestIsInQuietHours_SolarMode_NoSunCalc(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		sunCalc:    nil,
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled:     true,
		Mode:        "solar",
		StartEvent:  "sunset",
		StartOffset: 0,
		EndEvent:    "sunrise",
		EndOffset:   0,
	}

	assert.False(t, s.isInQuietHours(qh, time.Now()),
		"should return false when sunCalc is nil")
}

func TestIsInQuietHours_InvalidMode(t *testing.T) {
	t.Parallel()

	s := &QuietHoursScheduler{
		suppressed: make(map[string]bool),
	}

	qh := conf.QuietHoursConfig{
		Enabled: true,
		Mode:    "invalid",
	}

	assert.False(t, s.isInQuietHours(qh, time.Now()),
		"should return false for invalid mode")
}

func TestGetSolarEventTime(t *testing.T) {
	t.Parallel()

	sunTimes := suncalc.SunEventTimes{
		Sunrise:   time.Date(2025, 6, 15, 5, 30, 0, 0, time.Local),
		Sunset:    time.Date(2025, 6, 15, 20, 45, 0, 0, time.Local),
		CivilDawn: time.Date(2025, 6, 15, 5, 0, 0, 0, time.Local),
		CivilDusk: time.Date(2025, 6, 15, 21, 15, 0, 0, time.Local),
	}

	t.Run("sunrise", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(sunTimes, "sunrise")
		assert.True(t, got.Equal(sunTimes.Sunrise),
			"getSolarEventTime(sunrise) = %v, want %v", got, sunTimes.Sunrise)
	})

	t.Run("sunset", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(sunTimes, "sunset")
		assert.True(t, got.Equal(sunTimes.Sunset),
			"getSolarEventTime(sunset) = %v, want %v", got, sunTimes.Sunset)
	})

	t.Run("invalid event returns zero time", func(t *testing.T) {
		t.Parallel()
		got := getSolarEventTime(sunTimes, "invalid")
		assert.True(t, got.IsZero(), "getSolarEventTime(invalid) should return zero time")
	})
}
