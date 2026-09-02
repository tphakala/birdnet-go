// false_positive_filter.go
package processor

import (
	"math"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// birdBaseClipLength is the BirdNET reference analysis-clip length. Models with a
// different clip (e.g. Perch 5s) derive their false-positive step from their own
// clip instead of the 3s bird default.
const birdBaseClipLength = 3 * time.Second

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

// calculateMinDetectionsForModel routes to the correct minDetections calculation
// based on the model ID. Bat models use a fixed 50% overlap (1.5s step) instead
// of the user-configurable BirdNET overlap, and read from a separate filter
// config. For the 3s BirdNET model the bird path's step (3.0 - overlap) matches
// the buffer's cadence, which now honors birdnet.overlap (issue #4096).
func calculateMinDetectionsForModel(settings *conf.Settings, modelID string) int {
	if modelID == classifier.RegistryIDBat {
		return calculateBatMinDetections(settings)
	}
	// For a model whose analysis clip differs from the 3s BirdNET base (e.g. Perch
	// 5s), derive the step from the model's own clip and effective overlap so the
	// confirmation window matches the buffer cadence. The 3s bird path (and its
	// overlap-validation warnings) is preserved unchanged.
	if info, ok := classifier.ModelRegistry[modelID]; ok && info.Spec.ClipLength > 0 && info.Spec.ClipLength != birdBaseClipLength {
		step := info.Spec.BufferInterval(classifier.ResolveModelOverlap(modelID, info.Spec, settings)).Seconds()
		return minDetectionsForSegment(step, settings.Realtime.FalsePositiveFilter.Level)
	}
	return calculateMinDetectionsFromSettings(settings)
}

// Shared constants for the false-positive confirmation-count math.
const (
	// fpReferenceWindowSeconds is the typical duration of a bird vocalization;
	// minDetections is how many analysis windows within this window must confirm.
	fpReferenceWindowSeconds = 6.0
	// fpMinSegmentLength floors the analysis step to avoid dividing by ~0.
	fpMinSegmentLength = 0.1
	// fpEpsilon absorbs floating-point rounding before Ceil (e.g. 5.0000000003).
	fpEpsilon = 1e-9
	// batClipSeconds is the bat model's analysis clip length (3s). batOverlapSeconds
	// is its fixed 50% overlap, giving a 1.5s analysis step. The bat model's fixed
	// overlap is applied by classifier.ResolveModelOverlap (RegistryIDBat ->
	// ClipLength/2); these constants keep the FP confirmation math in step with it.
	batClipSeconds    = 3.0
	batOverlapSeconds = 1.5
)

// minDetectionsForSegment computes the minimum number of confirming detections
// required within the reference vocalization window, given the analysis step
// (segmentSeconds, i.e. how often a new analysis window is produced) and the
// filter level. Level 0 disables filtering (always 1). This is the single source
// of truth for the confirmation-count formula shared by the bird and bat paths.
func minDetectionsForSegment(segmentSeconds float64, level int) int {
	if level == 0 {
		return 1
	}
	// fpMinSegmentLength (0.1s) is coarser than the buffer's minAnalysisStep (1ms),
	// so the FP-assumed step and the real buffer step diverge once the step drops
	// below 0.1s, i.e. overlap > 2.9s on the 3s base clip. Overlap is driven by the
	// false-positive filter level, which caps at 2.8s (getMinimumOverlapForLevel(5)),
	// so the operational range never reaches that divergence; and it is conservative
	// (fewer confirmations required than windows produced), so no valid detection is
	// wrongly dropped even if it did.
	if segmentSeconds < fpMinSegmentLength {
		segmentSeconds = fpMinSegmentLength
	}
	maxDetections := fpReferenceWindowSeconds / segmentSeconds
	required := maxDetections*getThresholdForLevel(level) - fpEpsilon
	return int(math.Max(1, math.Ceil(required)))
}

// calculateBatMinDetections computes the minimum detection count for bat models.
// The bat model's buffer overlap is fixed at 50% (by classifier.ResolveModelOverlap,
// which returns ClipLength/2 for RegistryIDBat), giving a 1.5-second step for a
// 3-second clip. Within a 6-second reference window, this yields 4 possible
// detections.
func calculateBatMinDetections(settings *conf.Settings) int {
	return minDetectionsForSegment(batClipSeconds-batOverlapSeconds, settings.Bat.FalsePositiveFilter.Level)
}

// visibilityThresholds holds precomputed per-model visibility thresholds.
// The map key is the model ID; unknown model IDs fall back to the bird threshold.
type visibilityThresholds map[string]int

// precomputeVisibilityThresholds calculates visibility thresholds for bird and
// bat models once per settings snapshot so callers can look up by model ID
// without recomputing inside a loop or under a lock.
func precomputeVisibilityThresholds(settings *conf.Settings) visibilityThresholds {
	birdVis := CalculateVisibilityThreshold(calculateMinDetectionsFromSettings(settings))
	batVis := CalculateVisibilityThreshold(calculateBatMinDetections(settings))
	return visibilityThresholds{
		"":                       birdVis, // default for unknown model IDs
		classifier.RegistryIDBat: batVis,
	}
}

// getThreshold returns the visibility threshold for a model ID, falling back
// to the bird threshold for unknown model IDs.
func (vt visibilityThresholds) getThreshold(modelID string) int {
	if t, ok := vt[modelID]; ok {
		return t
	}
	return vt[""]
}

// getLevelDescription returns a detailed description of what each level does.
func getLevelDescription(level int) string {
	switch level {
	case 0:
		return "No filtering - accepts first detection immediately. Default for new installs, matches BirdNET-Pi sensitivity."
	case 1:
		return "Lenient filtering - requires 2 confirmations. Good for low-quality audio sources like RTSP surveillance cameras, webcam mics, or cheap USB microphones."
	case 2:
		return "Moderate filtering - requires 3 confirmations. Balanced for typical hobby setups with decent USB microphones."
	case 3:
		return "Balanced filtering - requires 5 confirmations. Original pre-September 2025 behavior, good for quality USB mics in average conditions."
	case 4:
		return "Strict filtering - requires 12 confirmations. Needs RPi 4+. For high-quality microphones capturing lots of environmental detail."
	case 5:
		return "Maximum filtering - requires 21 confirmations. Needs RPi 4+. For professional-grade microphones with high sensitivity that capture everything including wind, leaves, and distant sounds."
	default:
		return "Unknown filtering level"
	}
}
