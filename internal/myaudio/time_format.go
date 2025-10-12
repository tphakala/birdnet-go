package myaudio

import (
	"fmt"
	"time"
)

// FormatDuration formats a time.Duration into a human-readable string.
// It intelligently chooses the most appropriate unit and precision:
// - < 1 second: shows milliseconds (e.g., "450ms")
// - 1 second to < 1 minute: shows seconds without decimals (e.g., "45s")
// - 1 minute to < 1 hour: shows minutes and seconds (e.g., "2m 30s")
// - >= 1 hour: shows hours, minutes, and seconds (e.g., "1h 23m 45s")
//
// Durations are rounded to the nearest unit for more intuitive display:
//   - 11.4s rounds to "11s", 11.6s rounds to "12s"
//
// Examples:
//   - 450 * time.Millisecond -> "450ms"
//   - 11.46 * time.Second -> "11s"
//   - 2.5 * time.Minute -> "2m 30s"
//   - 1.5 * time.Hour -> "1h 30m 0s"
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", FormatDuration(-d))
	}

	// Less than 1 second: show milliseconds (rounded)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Round(time.Millisecond).Milliseconds())
	}

	// Less than 1 minute: show seconds only (rounded, no decimals)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Round(time.Second).Seconds()))
	}

	// Less than 1 hour: show minutes and seconds (rounded to nearest second)
	if d < time.Hour {
		d = d.Round(time.Second)
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	// 1 hour or more: show hours, minutes, and seconds (rounded to nearest second)
	d = d.Round(time.Second)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}
