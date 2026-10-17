// vocalization_labels.go adapts the shared vocalization label classifier to the
// processor's unexported call sites (privacy filter and dog bark filter). The
// classification logic and its rationale live in internal/labels/vocalization,
// the single source of truth shared with the classifier's top-K truncation.
package processor

import "github.com/tphakala/birdnet-go/internal/labels/vocalization"

// isHumanVocalization reports whether a raw classifier label represents a human
// voice sound (or any BirdNET "Human " class) that should engage the privacy
// filter. See vocalization.IsHuman.
func isHumanVocalization(rawLabel string) bool {
	return vocalization.IsHuman(rawLabel)
}

// isHumanSoundLabel reports whether a raw classifier label is any human sound,
// including the body and action classes the privacy filter ignores. The detection
// save filter uses it so those labels are still dropped as non-bird detections.
// See vocalization.IsHumanSound.
func isHumanSoundLabel(rawLabel string) bool {
	return vocalization.IsHumanSound(rawLabel)
}

// isDogDetection reports whether a raw classifier label represents a dog for the
// dog bark filter. See vocalization.IsDog.
func isDogDetection(rawLabel string) bool {
	return vocalization.IsDog(rawLabel)
}
