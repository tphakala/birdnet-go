// internal/suncalc/moon.go
package suncalc

import (
	"math"
	"time"

	"github.com/sj14/astral/pkg/astral"
)

// Moon phase name constants for the 8 standard lunar phases.
const (
	PhaseNewMoon        = "New Moon"
	PhaseWaxingCrescent = "Waxing Crescent"
	PhaseFirstQuarter   = "First Quarter"
	PhaseWaxingGibbous  = "Waxing Gibbous"
	PhaseFullMoon       = "Full Moon"
	PhaseWaningGibbous  = "Waning Gibbous"
	PhaseLastQuarter    = "Last Quarter"
	PhaseWaningCrescent = "Waning Crescent"
)

// Moon icon name constants matching Basmilius weather-icons.
const (
	IconNameNewMoon        = "moon-new"
	IconNameWaxingCrescent = "moon-waxing-crescent"
	IconNameFirstQuarter   = "moon-first-quarter"
	IconNameWaxingGibbous  = "moon-waxing-gibbous"
	IconNameFullMoon       = "moon-full"
	IconNameWaningGibbous  = "moon-waning-gibbous"
	IconNameLastQuarter    = "moon-last-quarter"
	IconNameWaningCrescent = "moon-waning-crescent"
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
	{1.75, PhaseNewMoon, IconNameNewMoon},
	{5.25, PhaseWaxingCrescent, IconNameWaxingCrescent},
	{8.75, PhaseFirstQuarter, IconNameFirstQuarter},
	{12.25, PhaseWaxingGibbous, IconNameWaxingGibbous},
	{15.75, PhaseFullMoon, IconNameFullMoon},
	{19.25, PhaseWaningGibbous, IconNameWaningGibbous},
	{22.75, PhaseLastQuarter, IconNameLastQuarter},
	{26.25, PhaseWaningCrescent, IconNameWaningCrescent},
	// Values >= 26.25 wrap back to New Moon (cycle boundary)
}

// moonPhaseEmojis maps phase names to their emoji representation.
var moonPhaseEmojis = map[string]string{
	PhaseNewMoon:        "🌑",
	PhaseWaxingCrescent: "🌒",
	PhaseFirstQuarter:   "🌓",
	PhaseWaxingGibbous:  "🌔",
	PhaseFullMoon:       "🌕",
	PhaseWaningGibbous:  "🌖",
	PhaseLastQuarter:    "🌗",
	PhaseWaningCrescent: "🌘",
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
