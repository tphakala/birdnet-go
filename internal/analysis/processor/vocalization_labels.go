// vocalization_labels.go classifies raw classifier labels as human or dog
// vocalizations for the privacy filter and the dog bark filter.
//
// Why match the RAW label (result.Species) instead of the enriched common name:
//
//  1. Locale stability. BirdNET loads a locale-specific label file, so for a
//     German user the dog class arrives as "Dog_Hund" and the human classes as
//     "Human vocal_Mensch Stimme" etc. SplitSpeciesName uses the part after "_"
//     as the common name ("Hund", "Mensch Stimme"), which contains no "dog" or
//     "human" token, so matching the enriched name silently failed for every
//     non-English locale. The part BEFORE the "_" (the scientific portion) is
//     always English ("Dog", "Human vocal", ...), so matching the raw label
//     works regardless of the configured locale.
//
//  2. Collision safety. A bare "human"/"dog" substring match also fires on bird
//     binomials that merely contain those letters, e.g. the cicada "Pacarina
//     schumanni" (...sc-human-ni) or the katydid "Poecilimon doga" (...doga).
//     Anchoring on the raw-label prefix ("human ", "dog_") excludes them.
//
//  3. Perch v2 (trained on iNaturalist 2024 + FSD50K) emits AudioSet-ontology
//     sound classes ("Speech", "Bark", "Growling", ...) that carry no "human"/
//     "dog" token at all, and whose underscore-joined forms ("Human_voice")
//     SplitSpeciesName mangles into "voice". The shared nonbird package matches
//     those full raw labels along with the iNaturalist human and dog taxa.
//
// Matching is case-insensitive, so a custom or future label file with different
// casing still engages the filters.
package processor

import (
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

// isHumanVocalization reports whether a raw classifier label represents a human
// sound that should engage the privacy filter. rawLabel is the untransformed
// result.Species value. Matching is case-insensitive.
func isHumanVocalization(rawLabel string) bool {
	return nonbird.IsHumanVocalization(rawLabel)
}

// isDogDetection reports whether a raw classifier label represents a dog for the
// dog bark filter. rawLabel is the untransformed result.Species value. Matching
// is case-insensitive: Perch v2 classes are matched exactly; BirdNET's "Dog"
// class is matched by the locale-stable English label prefix.
func isDogDetection(rawLabel string) bool {
	return nonbird.IsDogDetection(rawLabel)
}
