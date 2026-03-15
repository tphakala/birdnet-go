// internal/suncalc/moon.go
package suncalc

import (
	"math"
	"time"

	"github.com/sj14/astral/pkg/astral"
)

// MoonData holds computed moon phase information for a given date.
type MoonData struct {
	Phase        float64 // 0–27.99 raw phase value from astral library
	Illumination float64 // 0–100 approximate illumination percentage
	PhaseName    string  // One of 8 named phases
	IconName     string  // Basmilius weather-icons icon name
}

// phaseInfo maps a phase range to its name and icon.
type phaseInfo struct {
	maxPhase  float64
	phaseName string
	iconName  string
}

// phases defines the 8 standard lunar phases with their upper bounds.
//
// The astral library returns phase values in [0, 28) where:
//   - 0–6.99  = New moon quadrant
//   - 7–13.99 = First quarter quadrant
//   - 14–20.99 = Full moon quadrant
//   - 21–27.99 = Last quarter quadrant
//
// New moon occurs at 0 (and equivalently near 28, wrapping around).
// Full moon occurs near 14. The 8 sub-phases are evenly spaced at 3.5-day intervals,
// but New Moon straddles the cycle boundary: [26.25, 28) ∪ [0, 1.75).
var phases = []phaseInfo{
	{1.75, "New Moon", "moon-new"},
	{5.25, "Waxing Crescent", "moon-waxing-crescent"},
	{8.75, "First Quarter", "moon-first-quarter"},
	{12.25, "Waxing Gibbous", "moon-waxing-gibbous"},
	{15.75, "Full Moon", "moon-full"},
	{19.25, "Waning Gibbous", "moon-waning-gibbous"},
	{22.75, "Last Quarter", "moon-last-quarter"},
	{26.25, "Waning Crescent", "moon-waning-crescent"},
	// Values >= 26.25 wrap back to New Moon (cycle boundary)
}

// moonPhaseEmojis maps phase names to their emoji representation.
var moonPhaseEmojis = map[string]string{
	"New Moon":        "🌑",
	"Waxing Crescent": "🌒",
	"First Quarter":   "🌓",
	"Waxing Gibbous":  "🌔",
	"Full Moon":       "🌕",
	"Waning Gibbous":  "🌖",
	"Last Quarter":    "🌗",
	"Waning Crescent": "🌘",
}

// GetMoonPhase calculates the moon phase for the given date.
// The calculation is location-independent — only the date matters.
func GetMoonPhase(date time.Time) MoonData {
	phase := astral.MoonPhase(date)

	// Find the matching phase info.
	// New moon straddles the cycle boundary: phase >= 26.25 wraps back to new moon.
	var info phaseInfo
	if phase >= 26.25 {
		info = phases[0] // New Moon (wrap-around at end of cycle)
	} else {
		for _, p := range phases {
			if phase < p.maxPhase {
				info = p
				break
			}
		}
	}
	// Safety fallback (shouldn't be needed given astral's [0,28) range)
	if info.phaseName == "" {
		info = phases[0]
	}

	// Approximate illumination using cosine curve:
	// New moon (0) = 0%, Full moon (14) = 100%
	illumination := (1 - math.Cos(phase*2*math.Pi/28)) / 2 * 100

	return MoonData{
		Phase:        phase,
		Illumination: math.Round(illumination*10) / 10, // Round to 1 decimal
		PhaseName:    info.phaseName,
		IconName:     info.iconName,
	}
}

// MoonPhaseEmoji returns the emoji for a given phase name.
// Returns new moon emoji as fallback for unknown phase names.
func MoonPhaseEmoji(phaseName string) string {
	if emoji, ok := moonPhaseEmojis[phaseName]; ok {
		return emoji
	}
	return "🌑"
}
