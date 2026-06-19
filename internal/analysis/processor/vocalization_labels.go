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
//     the raw label here.
//
// Matching is case-insensitive: the raw label is lowercased once and compared
// against the lowercase keys / prefixes below, so a custom or future label file
// with different casing still engages the filters.
package processor

import "strings"

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

// perchHumanLabels enumerates the raw Perch v2 (FSD50K) labels treated as human
// vocalizations by the privacy filter. The set is deliberately broad: it covers
// the full AudioSet "Human sounds" branch present in the Perch label set (voice,
// other vocalizations, respiratory, digestive, hand, locomotion, and group
// actions) so any sign of a human near the microphone engages the filter, the
// same way BirdNET filters both "Human vocal" and "Human non-vocal". Non-human
// AudioSet classes that merely co-occur with people (e.g. "Car_passing_by",
// "Thump_and_thud") are intentionally excluded. Keys are lowercase; lookups
// lowercase the raw label first (see isHumanVocalization).
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchHumanLabels = map[string]struct{}{
	// Speech and voice.
	"speech":                           {},
	"speech_synthesizer":               {},
	"male_speech_and_man_speaking":     {},
	"female_speech_and_woman_speaking": {},
	"child_speech_and_kid_speaking":    {},
	"conversation":                     {},
	"chatter":                          {},
	"human_voice":                      {},
	"human_group_actions":              {},
	"whispering":                       {},
	"shout":                            {},
	"yell":                             {},
	"screaming":                        {},
	// Other human vocalizations.
	"singing":             {},
	"male_singing":        {},
	"female_singing":      {},
	"laughter":            {},
	"giggle":              {},
	"chuckle_and_chortle": {},
	"crying_and_sobbing":  {},
	"gasp":                {},
	"sigh":                {},
	// Non-vocal human body sounds (parallels BirdNET "Human non-vocal").
	"cough":                   {},
	"sneeze":                  {},
	"breathing":               {},
	"respiratory_sounds":      {},
	"burping_and_eructation":  {},
	"fart":                    {},
	"chewing_and_mastication": {},
	// Human taxon (iNaturalist) - humans detected as a species, not a sound.
	"homo sapiens": {},
	// Human actions and group sounds.
	"crowd":              {},
	"cheering":           {},
	"applause":           {},
	"clapping":           {},
	"finger_snapping":    {},
	"hands":              {},
	"walk_and_footsteps": {},
	"run":                {},
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
// result.Species value. Matching is case-insensitive: Perch v2 classes are
// matched exactly; BirdNET classes are matched by the locale-stable English
// label prefix.
func isHumanVocalization(rawLabel string) bool {
	lowered := strings.ToLower(rawLabel)
	if _, ok := perchHumanLabels[lowered]; ok {
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
