// vocalization_labels.go adapts the shared vocalization label classifier to the
// processor's unexported call sites (privacy filter and dog bark filter). The
// classification logic and its rationale live in internal/labels/vocalization,
// the single source of truth shared with the classifier's top-K truncation.
package processor

import "github.com/tphakala/birdnet-go/internal/labels/vocalization"

// isHumanVocalization reports whether a raw classifier label represents a human
// sound that should engage the privacy filter. See vocalization.IsHuman.
func isHumanVocalization(rawLabel string) bool {
	return vocalization.IsHuman(rawLabel)
}

// isDogDetection reports whether a raw classifier label represents a dog for the
// dog bark filter. See vocalization.IsDog.
func isDogDetection(rawLabel string) bool {
	return vocalization.IsDog(rawLabel)
}
