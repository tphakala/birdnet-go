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
// Examples:
//   - 450 * time.Millisecond -> "450ms"
//   - 11.46 * time.Second -> "11s"
//   - 2.5 * time.Minute -> "2m 30s"
//   - 1.5 * time.Hour -> "1h 30m 0s"
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", FormatDuration(-d))
	}

	// Less than 1 second: show milliseconds
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	// Less than 1 minute: show seconds only (no decimals)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	// Less than 1 hour: show minutes and seconds
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	// 1 hour or more: show hours, minutes, and seconds
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// FormatDurationCompact formats a time.Duration into a compact human-readable string
// suitable for inline display. Similar to FormatDuration but more concise:
// - < 1 second: shows milliseconds (e.g., "450ms")
// - 1 second to < 1 minute: shows seconds without decimals (e.g., "45s")
// - 1 minute to < 1 hour: shows minutes with 1 decimal (e.g., "2.5m")
// - >= 1 hour: shows hours with 1 decimal (e.g., "1.5h")
//
// This format is best for progress messages and space-constrained displays.
func FormatDurationCompact(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", FormatDurationCompact(-d))
	}

	// Less than 1 second: show milliseconds
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	// Less than 1 minute: show seconds only (no decimals)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	// Less than 1 hour: show minutes with 1 decimal
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}

	// 1 hour or more: show hours with 1 decimal
	return fmt.Sprintf("%.1fh", d.Hours())
}
