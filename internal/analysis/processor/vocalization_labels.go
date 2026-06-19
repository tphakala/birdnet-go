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
//     SplitSpeciesName mangles into "voice". Those are matched exactly against
//     the raw label here. The Perch/AudioSet (FSD50K) human-sound classes are
//     handled by the shared nonbird package (CategoryHuman); only the
//     iNaturalist taxon "homo sapiens" requires a local extra-labels map.
//
// Matching is case-insensitive: the raw label is lowercased once and compared
// against the lowercase keys / prefixes below, so a custom or future label file
// with different casing still engages the filters.
package processor

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
)

const (
	// birdnetHumanLabelPrefix matches BirdNET's human classes by the English
	// scientific portion of the raw label ("Human vocal_...", "Human non-vocal_...",
	// "Human whistle_..."), which is the same across every locale. The trailing
	// space is load-bearing: "human vocal" matches, but the cicada "Pacarina
	// schumanni" (which contains the substring "human") does not.
	birdnetHumanLabelPrefix = "human "
	// birdnetDogLabelPrefix matches BirdNET's "Dog" class by its raw-label form
	// "Dog_<localized common name>" (e.g. "Dog_Dog", "Dog_Hund"). The underscore
	// is load-bearing: the class matches, but the katydid "Poecilimon doga"
	// (which contains the substring "dog") does not.
	birdnetDogLabelPrefix = "dog_"
)

// perchHumanExtraLabels holds human labels that the shared nonbird package does
// not cover: iNaturalist taxa (not AudioSet/FSD50K sound classes). Keys are
// lowercase.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchHumanExtraLabels = map[string]struct{}{
	"homo sapiens": {}, // human detected as a species (iNaturalist taxon), not a sound class
}

// perchDogLabels enumerates the raw Perch v2 labels treated as a dog by the dog
// bark filter: the FSD50K dog sound classes plus the domestic dog taxon. Wild
// canids (wolf, coyote, jackal) are intentionally excluded so they remain
// detectable as wildlife rather than being suppressed as background barking.
// "Growling" is the AudioSet child of the Dog class (dog growling), so it stays.
// Keys are lowercase; lookups lowercase the raw label first (see isDogDetection).
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchDogLabels = map[string]struct{}{
	"dog":              {}, // FSD50K dog
	"bark":             {}, // FSD50K bark
	"growling":         {}, // FSD50K dog growl (AudioSet child of Dog)
	"canis familiaris": {}, // domestic dog (iNaturalist taxon)
}

// isHumanVocalization reports whether a raw classifier label represents a human
// sound that should engage the privacy filter. rawLabel is the untransformed
// result.Species value. Matching is case-insensitive.
//
// The AudioSet/FSD50K portion is delegated to the shared nonbird package
// (nonbird.CategoryHuman). The iNaturalist taxon "homo sapiens" and the
// BirdNET locale-stable prefix are handled locally.
func isHumanVocalization(rawLabel string) bool {
	if cat, ok := nonbird.CategoryOf(rawLabel); ok && cat == nonbird.CategoryHuman {
		return true
	}
	lowered := strings.ToLower(rawLabel)
	if _, ok := perchHumanExtraLabels[lowered]; ok {
		return true
	}
	return strings.HasPrefix(lowered, birdnetHumanLabelPrefix)
}

// isDogDetection reports whether a raw classifier label represents a dog for the
// dog bark filter. rawLabel is the untransformed result.Species value. Matching
// is case-insensitive: Perch v2 classes are matched exactly; BirdNET's "Dog"
// class is matched by the locale-stable English label prefix.
func isDogDetection(rawLabel string) bool {
	lowered := strings.ToLower(rawLabel)
	if _, ok := perchDogLabels[lowered]; ok {
		return true
	}
	return strings.HasPrefix(lowered, birdnetDogLabelPrefix)
}
