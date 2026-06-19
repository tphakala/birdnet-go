package nonbird

import "strings"

// Category names the kind of non-bird sound class a label represents.
type Category string

const (
	// CategoryHuman covers human vocal and body sounds (speech, laughter, breathing, etc.).
	CategoryHuman Category = "human"
	// CategoryAnimal covers non-bird animal sounds (dog, cat, insect, etc.).
	CategoryAnimal Category = "animal"
	// CategoryMusic covers musical instruments and performed music.
	CategoryMusic Category = "music"
	// CategoryMechanical covers vehicles, tools, appliances, and other mechanical sources.
	CategoryMechanical Category = "mechanical"
	// CategoryEnvironment covers natural environmental sounds (rain, wind, water, fire).
	CategoryEnvironment Category = "environment"
	// CategoryNoise covers unstructured noise events (buzz, crack, hiss, shatter, etc.).
	CategoryNoise Category = "noise"
	// CategoryDevice covers electronic devices and household appliances.
	CategoryDevice Category = "device"
)

// firstTokenSet holds the first underscore-delimited token of every multi-word key in classes.
// Keys are split on the first "_" only, so a token may itself contain a hyphen
// (e.g. "fixed-wing" from "fixed-wing_aircraft_and_airplane"). It is derived once in
// init() and never modified after that.
var firstTokenSet map[string]struct{}

func init() {
	firstTokenSet = make(map[string]struct{})
	for k := range classes {
		if before, _, found := strings.Cut(k, "_"); found {
			firstTokenSet[before] = struct{}{}
		}
	}
}

// CategoryOf returns the category for a FULL raw model label (e.g. "power_tool",
// "male_speech_and_man_speaking"). The match is exact against the known class set,
// case-insensitive. It does NOT match truncated first-token forms; callers that only
// have the first token (the image provider) must use IsNonBirdName instead.
// ok is false for bird species and any unknown label.
func CategoryOf(rawLabel string) (Category, bool) {
	cat, ok := classes[strings.ToLower(rawLabel)]
	return cat, ok
}

// IsNonSpeciesLabel reports whether rawLabel is a known non-bird sound class
// (the full-label exact-match path). Equivalent to: _, ok := CategoryOf(rawLabel); ok.
func IsNonSpeciesLabel(rawLabel string) bool {
	_, ok := CategoryOf(rawLabel)
	return ok
}

// IsNonBirdName reports whether name is a non-bird class, matching EITHER the full
// label OR the first token of a multi-word (underscore-joined) class. The image
// provider only receives the underscore-split first token of a label (e.g. "Power"
// from "power_tool", "Engine" from "engine"), so this is the lookup it needs.
// Case-insensitive.
func IsNonBirdName(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := classes[lower]; ok {
		return true
	}
	_, ok := firstTokenSet[lower]
	return ok
}
