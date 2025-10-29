// falsepositive.go
package processor

// getMinimumOverlapForLevel returns the minimum overlap required for each filtering level.
// Higher levels require higher overlap to generate more detections for filtering.
//
// Hardware limits:
//   - RPi 3B/Zero 2: ~541ms inference time, max overlap 2.4 (600ms steps)
//   - RPi 4: ~166ms inference time, max overlap 2.8 (200ms steps)
//   - RPi 5: ~100ms inference time, max overlap 2.9+ (100ms steps)
func getMinimumOverlapForLevel(level int) float64 {
	switch level {
	case 0:
		return 0.0 // Any overlap (filtering disabled)
	case 1:
		return 2.0 // Lenient (1000ms steps)
	case 2:
		return 2.2 // Moderate (800ms steps)
	case 3:
		return 2.4 // Balanced (600ms steps) - RPi 3B max
	case 4:
		return 2.7 // Strict (300ms steps) - RPi 4 required
	case 5:
		return 2.8 // Maximum (200ms steps) - RPi 4 required
	default:
		return 2.2 // Default to Moderate
	}
}

// getThresholdForLevel returns the percentage threshold of required confirmations
// within the reference window (6 seconds) for each filtering level.
//
// The threshold determines what percentage of possible detections must match
// for a detection to be considered valid (not a false positive).
func getThresholdForLevel(level int) float64 {
	switch level {
	case 0:
		return 0.0 // No filtering (0% of possible detections = min 1 detection)
	case 1:
		return 0.20 // 20% of 6s window
	case 2:
		return 0.30 // 30% of 6s window
	case 3:
		return 0.50 // 50% of 6s window (ORIGINAL pre-September 2025 behavior)
	case 4:
		return 0.60 // 60% of 6s window
	case 5:
		return 0.70 // 70% of 6s window
	default:
		return 0.30 // Default to Moderate
	}
}

// getHardwareRequirementForLevel returns a human-readable description of
// the hardware requirements for each filtering level.
func getHardwareRequirementForLevel(level int) string {
	switch level {
	case 0, 1, 2, 3:
		return "Any (RPi 3B or better)"
	case 4, 5:
		return "RPi 4 or better required"
	default:
		return "Unknown"
	}
}

// getLevelName returns the human-readable name for each filtering level.
func getLevelName(level int) string {
	switch level {
	case 0:
		return "Off"
	case 1:
		return "Lenient"
	case 2:
		return "Moderate"
	case 3:
		return "Balanced"
	case 4:
		return "Strict"
	case 5:
		return "Maximum"
	default:
		return "Unknown"
	}
}

// getRecommendedLevelForOverlap suggests the appropriate filtering level
// based on the user's current overlap setting. This is used for smart migration
// when users are upgrading from versions without level-based filtering.
//
// Returns the recommended level and a boolean indicating if the overlap is sufficient.
func getRecommendedLevelForOverlap(overlap float64) (level int, overlapSufficient bool) {
	// Find the highest level that the overlap can support
	for testLevel := 5; testLevel >= 0; testLevel-- {
		minOverlap := getMinimumOverlapForLevel(testLevel)
		if overlap >= minOverlap {
			// Found a level that works with this overlap
			return testLevel, true
		}
	}

	// If we get here, overlap is too low even for Level 0
	// This shouldn't happen since Level 0 requires 0.0 overlap
	return 0, true
}

// getLevelDescription returns a detailed description of what each level does.
func getLevelDescription(level int) string {
	switch level {
	case 0:
		return "No filtering - accepts first detection immediately. Use for testing or very quiet environments."
	case 1:
		return "Lenient filtering - requires 2 confirmations. Good for very noisy urban environments."
	case 2:
		return "Moderate filtering - requires 3 confirmations. Current default, balanced for most users."
	case 3:
		return "Balanced filtering - requires 5 confirmations. Restores original (pre-September 2025) behavior."
	case 4:
		return "Strict filtering - requires 12 confirmations. Needs RPi 4+. For quiet environments and research."
	case 5:
		return "Maximum filtering - requires 21 confirmations. Needs RPi 4+. For laboratory settings with extreme noise."
	default:
		return "Unknown filtering level"
	}
}
