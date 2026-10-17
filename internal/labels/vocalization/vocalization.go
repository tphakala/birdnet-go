// Package vocalization classifies raw classifier labels as human or dog
// vocalizations. It is the single source of truth for the privacy filter and
// the dog-bark filter (which consume it in the analysis processor) and for the
// classifier's top-K truncation, which retains these classes so a low-confidence
// speech or dog prediction is not hidden from the filters (issue #4177).
//
// Among the Perch/AudioSet labels, the privacy filter's human set is speech and
// speech-like voicing only (IsHuman, Classify): such a class engages it only
// when a person is voicing, i.e. speech, conversation, singing, laughter,
// crying, shouting, whispering, cheering, plus the "homo sapiens" taxon. Every
// BirdNET "Human " class (vocal, non-vocal and whistle alike, matched by
// prefix) engages it too, unchanged. The AudioSet/FSD50K body and action
// classes in nonbird.CategoryHuman that are not voiced (privacyExcludedLabels:
// hands, chewing, footsteps, running, clapping, applause, finger snapping,
// farting, burping, crowd, human group actions, and the non-voiced bodily
// sounds cough, sneeze, breathing, respiratory sounds, sigh, gasp) do not
// engage it. The first eleven fire on ordinary non-speech sounds and caused
// measured privacy false positives; the bodily sounds are excluded by
// maintainer decision (they are not speech), not by measurement. They stay
// nonbird.CategoryHuman, and IsHumanSound still reports them as human sounds so
// the processor keeps dropping them as non-bird detections.
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
package vocalization

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

// privacyExcludedLabels holds the nonbird.CategoryHuman classes that are body or
// action sounds rather than voiced speech-like sound. They are not privacy hits
// (see IsHuman) but remain human sounds (see IsHumanSound). Keys are lowercase.
// A replay of Perch V2 on user-verified clips found the first eleven behind the
// false "Human vocal" privacy discards while speech clips were still caught; the
// six bodily sounds were not measured.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var privacyExcludedLabels = map[string]struct{}{
	"human_group_actions":     {},
	"hands":                   {},
	"chewing_and_mastication": {},
	"walk_and_footsteps":      {},
	"run":                     {},
	"clapping":                {},
	"applause":                {},
	"finger_snapping":         {},
	"fart":                    {},
	"burping_and_eructation":  {},
	"crowd":                   {},
	// Non-voiced bodily sounds, excluded by maintainer decision. The replay did
	// not validate them either way (none drove a false positive or a catch).
	"cough":              {},
	"sneeze":             {},
	"breathing":          {},
	"respiratory_sounds": {},
	"sigh":               {},
	"gasp":               {},
}

// perchDogLabels enumerates the raw Perch v2 labels treated as a dog by the dog
// bark filter: the FSD50K dog sound classes plus the domestic dog taxon. Wild
// canids (wolf, coyote, jackal) are intentionally excluded so they remain
// detectable as wildlife rather than being suppressed as background barking.
// "Growling" is the AudioSet child of the Dog class (dog growling), so it stays.
// Keys are lowercase; lookups lowercase the raw label first (see classifyLowered).
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchDogLabels = map[string]struct{}{
	"dog":              {}, // FSD50K dog
	"bark":             {}, // FSD50K bark
	"growling":         {}, // FSD50K dog growl (AudioSet child of Dog)
	"canis familiaris": {}, // domestic dog (iNaturalist taxon)
}

// Classify reports whether a raw classifier label represents a human voice
// sound or any BirdNET "Human " class (privacy filter; excludes
// privacyExcludedLabels) and/or a dog (dog-bark filter) in a single pass.
// rawLabel is the untransformed result.Species value. Matching is case-insensitive.
//
// It lowercases rawLabel ONCE and answers both questions from the lowered form,
// so the classifier's per-inference truncation (which evaluates every one of the
// ~6.5k-15k predictions) pays a single allocation per label instead of the three
// that separate IsHuman + IsDog calls would (nonbird.CategoryOf re-lowering an
// already-lowercase ASCII string is allocation-free).
func Classify(rawLabel string) (human, dog bool) {
	return classifyLowered(strings.ToLower(rawLabel))
}

// classifyLowered is the shared core: it takes an already-lowercased label and
// returns the human and dog verdicts. The human verdict is isHumanSoundLowered
// (the AudioSet/FSD50K portion comes from the shared nonbird package, the
// iNaturalist taxon "homo sapiens" and the BirdNET locale-stable prefix are
// handled locally) minus privacyExcludedLabels, so it is the privacy filter's
// voiced-sound set for Perch labels plus the BirdNET "Human " prefix. The dog
// check matches Perch v2 classes exactly and BirdNET's "Dog" class by its
// locale-stable English label prefix.
func classifyLowered(lowered string) (human, dog bool) {
	human = isHumanSoundLowered(lowered) && !mapHas(privacyExcludedLabels, lowered)
	switch {
	case mapHas(perchDogLabels, lowered):
		dog = true
	case strings.HasPrefix(lowered, birdnetDogLabelPrefix):
		dog = true
	}
	return human, dog
}

// isHumanSoundLowered reports whether the (already-lowercased) label is any human
// sound: a nonbird.CategoryHuman class, the "homo sapiens" taxon or a BirdNET
// "Human " class. It does not apply privacyExcludedLabels.
func isHumanSoundLowered(lowered string) bool {
	return categoryIsHuman(lowered) ||
		mapHas(perchHumanExtraLabels, lowered) ||
		strings.HasPrefix(lowered, birdnetHumanLabelPrefix)
}

// categoryIsHuman reports whether the (already-lowercased) label is an AudioSet/
// FSD50K human sound class per the shared nonbird package.
func categoryIsHuman(lowered string) bool {
	cat, ok := nonbird.CategoryOf(lowered)
	return ok && cat == nonbird.CategoryHuman
}

func mapHas(m map[string]struct{}, key string) bool {
	_, ok := m[key]
	return ok
}

// IsHuman reports whether a raw classifier label represents a human voice sound
// (or any BirdNET "Human " class) that should engage the privacy filter. Body
// and action classes are excluded, see privacyExcludedLabels and Classify.
func IsHuman(rawLabel string) bool {
	human, _ := Classify(rawLabel)
	return human
}

// IsDog reports whether a raw classifier label represents a dog for the dog bark
// filter. See Classify.
func IsDog(rawLabel string) bool {
	_, dog := Classify(rawLabel)
	return dog
}

// IsHumanSound reports whether a raw classifier label is any human sound,
// including the body and action classes the privacy filter excludes (see
// privacyExcludedLabels). The processor uses it to keep dropping such labels as
// non-bird detections; use IsHuman for the privacy filter.
func IsHumanSound(rawLabel string) bool {
	return isHumanSoundLowered(strings.ToLower(rawLabel))
}
