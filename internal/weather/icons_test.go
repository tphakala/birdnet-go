package weather

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

func TestGetWeatherIcon(t *testing.T) {
	tests := []struct {
		name      string
		code      IconCode
		timeOfDay TimeOfDay
		wantSVG   bool
	}{
		// Clear sky - all times of day
		{"clear_sky_day", IconClearSky, Day, true},
		{"clear_sky_night", IconClearSky, Night, true},
		{"clear_sky_dawn", IconClearSky, Dawn, true},
		{"clear_sky_dusk", IconClearSky, Dusk, true},

		// Fair weather
		{"fair_day", IconFair, Day, true},
		{"fair_night", IconFair, Night, true},

		// Partly cloudy
		{"partly_cloudy_day", IconPartlyCloudy, Day, true},
		{"partly_cloudy_night", IconPartlyCloudy, Night, true},

		// Cloudy
		{"cloudy_day", IconCloudy, Day, true},
		{"cloudy_night", IconCloudy, Night, true},

		// Rain showers
		{"rain_showers_day", IconRainShowers, Day, true},

		// Rain
		{"rain_day", IconRain, Day, true},
		{"rain_night", IconRain, Night, true},

		// Thunderstorm
		{"thunderstorm_day", IconThunderstorm, Day, true},

		// Sleet
		{"sleet_day", IconSleet, Day, true},
		{"sleet_night", IconSleet, Night, true},

		// Snow
		{"snow_day", IconSnow, Day, true},
		{"snow_night", IconSnow, Night, true},

		// Fog
		{"fog_day", IconFog, Day, true},

		// Unknown code
		{"unknown_code", IconUnknown, Day, true}, // Should return default icon
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon := GetWeatherIcon(tt.code, tt.timeOfDay)
			iconStr := string(icon)

			if tt.wantSVG {
				assert.True(t, strings.HasPrefix(iconStr, "<svg"), "Icon should be SVG")
				assert.True(t, strings.HasSuffix(iconStr, "</svg>"), "Icon should end with </svg>")
			}
		})
	}
}

func TestGetWeatherIcon_Fallback(t *testing.T) {
	// Test fallback to Day icon when specific time variant doesn't exist
	t.Run("falls_back_to_day_version", func(t *testing.T) {
		// Rain showers only has Day version in the map
		dayIcon := GetWeatherIcon(IconRainShowers, Day)
		nightIcon := GetWeatherIcon(IconRainShowers, Night)

		// Night should fall back to Day
		assert.Equal(t, dayIcon, nightIcon, "Night should fall back to Day version")
	})

	t.Run("unknown_icon_code_returns_default", func(t *testing.T) {
		icon := GetWeatherIcon("nonexistent", Day)
		iconStr := string(icon)

		assert.True(t, strings.HasPrefix(iconStr, "<svg"), "Should return a default SVG icon")
	})
}

func TestGetIconDescription(t *testing.T) {
	tests := []struct {
		name     string
		code     IconCode
		expected string
	}{
		{"clear_sky", IconClearSky, "Clear Sky"},
		{"fair", IconFair, "Fair"},
		{"partly_cloudy", IconPartlyCloudy, "Partly Cloudy"},
		{"cloudy", IconCloudy, "Cloudy"},
		{"rain_showers", IconRainShowers, "Rain Showers"},
		{"rain", IconRain, "Rain"},
		{"thunderstorm", IconThunderstorm, "Thunderstorm"},
		{"sleet", IconSleet, "Sleet"},
		{"snow", IconSnow, "Snow"},
		{"fog", IconFog, "Fog"},
		{"unknown", IconUnknown, "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desc := GetIconDescription(tt.code)
			assert.Equal(t, tt.expected, desc)
		})
	}

	t.Run("nonexistent_code_returns_unknown", func(t *testing.T) {
		desc := GetIconDescription("nonexistent")
		assert.Equal(t, "Unknown", desc)
	})
}

func TestGetTimeOfDayIcon(t *testing.T) {
	tests := []struct {
		name      string
		timeOfDay TimeOfDay
		wantSVG   bool
	}{
		{"day", Day, true},
		{"night", Night, true},
		{"dawn", Dawn, true},
		{"dusk", Dusk, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			icon := GetTimeOfDayIcon(tt.timeOfDay)
			iconStr := string(icon)

			if tt.wantSVG {
				assert.True(t, strings.HasPrefix(iconStr, "<svg"), "Icon should be SVG")
				assert.True(t, strings.HasSuffix(iconStr, "</svg>"), "Icon should end with </svg>")
			}
		})
	}

	t.Run("invalid_time_returns_default", func(t *testing.T) {
		// Test with an invalid TimeOfDay value
		icon := GetTimeOfDayIcon(TimeOfDay(999))
		iconStr := string(icon)
		assert.True(t, strings.HasPrefix(iconStr, "<svg"), "Should return a default SVG icon")
	})
}

func TestCalculateTimeOfDay(t *testing.T) {
	// Create test sun events for a typical day
	// Using UTC for simplicity
	sunEvents := &suncalc.SunEventTimes{
		CivilDawn: time.Date(2026, 1, 13, 6, 0, 0, 0, time.UTC),  // 06:00
		Sunrise:   time.Date(2026, 1, 13, 7, 0, 0, 0, time.UTC),  // 07:00
		Sunset:    time.Date(2026, 1, 13, 17, 0, 0, 0, time.UTC), // 17:00
		CivilDusk: time.Date(2026, 1, 13, 18, 0, 0, 0, time.UTC), // 18:00
	}

	tests := []struct {
		name     string
		noteTime time.Time
		expected TimeOfDay
	}{
		// Night - before civil dawn
		{
			name:     "early_morning_night",
			noteTime: time.Date(2026, 1, 13, 3, 0, 0, 0, time.UTC),
			expected: Night,
		},
		{
			name:     "just_before_civil_dawn",
			noteTime: time.Date(2026, 1, 13, 5, 59, 0, 0, time.UTC),
			expected: Night,
		},

		// Dawn - between civil dawn and sunrise
		{
			name:     "at_civil_dawn",
			noteTime: time.Date(2026, 1, 13, 6, 0, 0, 0, time.UTC),
			expected: Dawn,
		},
		{
			name:     "mid_dawn",
			noteTime: time.Date(2026, 1, 13, 6, 30, 0, 0, time.UTC),
			expected: Dawn,
		},
		{
			name:     "just_before_sunrise",
			noteTime: time.Date(2026, 1, 13, 6, 59, 0, 0, time.UTC),
			expected: Dawn,
		},

		// Day - between sunrise and sunset
		{
			name:     "at_sunrise",
			noteTime: time.Date(2026, 1, 13, 7, 0, 0, 0, time.UTC),
			expected: Day,
		},
		{
			name:     "mid_day",
			noteTime: time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC),
			expected: Day,
		},
		{
			name:     "just_before_sunset",
			noteTime: time.Date(2026, 1, 13, 16, 59, 0, 0, time.UTC),
			expected: Day,
		},

		// Dusk - between sunset and civil dusk
		{
			name:     "at_sunset",
			noteTime: time.Date(2026, 1, 13, 17, 0, 0, 0, time.UTC),
			expected: Dusk,
		},
		{
			name:     "mid_dusk",
			noteTime: time.Date(2026, 1, 13, 17, 30, 0, 0, time.UTC),
			expected: Dusk,
		},
		{
			name:     "just_before_civil_dusk",
			noteTime: time.Date(2026, 1, 13, 17, 59, 0, 0, time.UTC),
			expected: Dusk,
		},

		// Night - after civil dusk
		{
			name:     "at_civil_dusk",
			noteTime: time.Date(2026, 1, 13, 18, 0, 0, 0, time.UTC),
			expected: Night,
		},
		{
			name:     "evening_night",
			noteTime: time.Date(2026, 1, 13, 22, 0, 0, 0, time.UTC),
			expected: Night,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateTimeOfDay(tt.noteTime, sunEvents)
			assert.Equal(t, tt.expected, result, "CalculateTimeOfDay returned unexpected result")
		})
	}
}

func TestStringToTimeOfDay(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected TimeOfDay
	}{
		{"night", "night", Night},
		{"dawn", "dawn", Dawn},
		{"day", "day", Day},
		{"dusk", "dusk", Dusk},
		{"unknown_defaults_to_day", "unknown", Day},
		{"empty_defaults_to_day", "", Day},
		{"capitalized_defaults_to_day", "Day", Day}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StringToTimeOfDay(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTimeOfDayConstants(t *testing.T) {
	// Verify TimeOfDay constants are unique
	values := map[TimeOfDay]bool{
		Day:   true,
		Night: true,
		Dawn:  true,
		Dusk:  true,
	}

	require.Len(t, values, 4, "All TimeOfDay values should be unique")

	// Verify Day starts at 0 (iota)
	assert.Equal(t, Day, TimeOfDay(0))
	assert.Equal(t, Night, TimeOfDay(1))
	assert.Equal(t, Dawn, TimeOfDay(2))
	assert.Equal(t, Dusk, TimeOfDay(3))
}

func TestWeatherIconsMapCoverage(t *testing.T) {
	// Verify that all expected icon codes have entries in the map
	expectedCodes := []IconCode{
		IconClearSky,     // "01"
		IconFair,         // "02"
		IconPartlyCloudy, // "03"
		IconCloudy,       // "04"
		IconRainShowers,  // "09"
		IconRain,         // "10"
		IconThunderstorm, // "11"
		IconSleet,        // "12"
		IconSnow,         // "13"
		IconFog,          // "50"
	}

	for _, code := range expectedCodes {
		t.Run(string(code), func(t *testing.T) {
			_, exists := weatherIcons[code]
			assert.True(t, exists, "weatherIcons should have entry for %q", code)
		})
	}
}

func TestTimeOfDayIconsMapCoverage(t *testing.T) {
	// Verify all TimeOfDay values have icons
	timesOfDay := []TimeOfDay{Day, Night, Dawn, Dusk}

	for _, tod := range timesOfDay {
		t.Run(string(rune('0'+tod)), func(t *testing.T) {
			_, exists := timeOfDayIcons[tod]
			assert.True(t, exists, "timeOfDayIcons should have entry for TimeOfDay %d", tod)
		})
	}
}
