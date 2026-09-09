// Package formatutil holds small, dependency-free formatting helpers shared
// across the codebase. It is a leaf package (no other internal imports) so that
// low-level packages like diskmanager can use it without creating an import
// cycle through the API layer, where some of these helpers previously lived as
// duplicates.
package formatutil

import "fmt"

// byteUnit is the base used to step between magnitude prefixes (binary KiB, so
// 1024), matching every prior copy of this helper so the rendered output is
// unchanged.
const byteUnit = 1024

// bytePrefixes are the magnitude prefixes above bytes. The loop that walks them
// is bounded by their count so the index can never run past 'E' regardless of
// the input magnitude.
const bytePrefixes = "KMGTPE"

// Bytes renders a signed byte count as a short human-readable string (e.g.
// "150.0 MB", "512 B"). Values below one unit are rendered as a plain byte
// count, so a negative input is printed verbatim (e.g. "-5 B"); callers that
// can produce negatives and want them clamped should guard before calling.
func Bytes(b int64) string {
	if b < byteUnit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(byteUnit), 0
	for n := b / byteUnit; n >= byteUnit && exp < len(bytePrefixes)-1; n /= byteUnit {
		div *= byteUnit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), bytePrefixes[exp])
}

// BytesUint64 renders an unsigned byte count as a short human-readable string,
// identical in format to Bytes but for uint64 inputs (used where a size is
// naturally unsigned, e.g. database and backup byte totals).
func BytesUint64(b uint64) string {
	if b < byteUnit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(byteUnit), 0
	for n := b / byteUnit; n >= byteUnit && exp < len(bytePrefixes)-1; n /= byteUnit {
		div *= byteUnit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), bytePrefixes[exp])
}
