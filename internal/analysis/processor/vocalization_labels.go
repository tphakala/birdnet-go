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
// "Thump_and_thud") are intentionally excluded.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchHumanLabels = map[string]struct{}{
	// Speech and voice.
	"Speech":                           {},
	"Speech_synthesizer":               {},
	"Male_speech_and_man_speaking":     {},
	"Female_speech_and_woman_speaking": {},
	"Child_speech_and_kid_speaking":    {},
	"Conversation":                     {},
	"Chatter":                          {},
	"Human_voice":                      {},
	"Human_group_actions":              {},
	"Whispering":                       {},
	"Shout":                            {},
	"Yell":                             {},
	"Screaming":                        {},
	// Other human vocalizations.
	"Singing":             {},
	"Male_singing":        {},
	"Female_singing":      {},
	"Laughter":            {},
	"Giggle":              {},
	"Chuckle_and_chortle": {},
	"Crying_and_sobbing":  {},
	"Gasp":                {},
	"Sigh":                {},
	// Non-vocal human body sounds (parallels BirdNET "Human non-vocal").
	"Cough":                   {},
	"Sneeze":                  {},
	"Breathing":               {},
	"Respiratory_sounds":      {},
	"Burping_and_eructation":  {},
	"Fart":                    {},
	"Chewing_and_mastication": {},
	// Human taxon (iNaturalist) - humans detected as a species, not a sound.
	"Homo sapiens": {},
	// Human actions and group sounds.
	"Crowd":              {},
	"Cheering":           {},
	"Applause":           {},
	"Clapping":           {},
	"Finger_snapping":    {},
	"Hands":              {},
	"Walk_and_footsteps": {},
	"Run":                {},
}

// perchDogLabels enumerates the raw Perch v2 labels treated as a dog by the dog
// bark filter: the FSD50K dog sound classes plus the domestic dog taxon. Wild
// canids (wolf, coyote, jackal) are intentionally excluded so they remain
// detectable as wildlife rather than being suppressed as background barking.
//
//nolint:gochecknoglobals // immutable lookup table, read-only after init
var perchDogLabels = map[string]struct{}{
	"Dog":              {}, // FSD50K dog
	"Bark":             {}, // FSD50K bark
	"Growling":         {}, // FSD50K growl
	"Canis familiaris": {}, // domestic dog (iNaturalist taxon)
}

// isHumanVocalization reports whether a raw classifier label represents a human
// sound that should engage the privacy filter. rawLabel is the untransformed
// result.Species value. Perch v2 classes are matched exactly; BirdNET classes
// are matched by the locale-stable English label prefix.
func isHumanVocalization(rawLabel string) bool {
	if _, ok := perchHumanLabels[rawLabel]; ok {
		return true
	}
	return strings.HasPrefix(strings.ToLower(rawLabel), birdnetHumanLabelPrefix)
}

// isDogDetection reports whether a raw classifier label represents a dog for the
// dog bark filter. rawLabel is the untransformed result.Species value. Perch v2
// classes are matched exactly; BirdNET's "Dog" class is matched by the
// locale-stable English label prefix.
func isDogDetection(rawLabel string) bool {
	if _, ok := perchDogLabels[rawLabel]; ok {
		return true
	}
	return strings.HasPrefix(strings.ToLower(rawLabel), birdnetDogLabelPrefix)
}
