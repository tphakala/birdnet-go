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

	humanLabelPrefix = "human "
	humanTaxonLabel  = "homo sapiens"
	dogLabelPrefix   = "dog_"
	dogLabel         = "dog"
	dogBarkLabel     = "bark"
	dogGrowlingLabel = "growling"
	dogTaxonLabel    = "canis familiaris"
)

// firstTokenSet holds the first underscore-delimited token of every multi-word key in classes.
// Keys are split on the first "_" only, so a token may itself contain a hyphen
// (e.g. "fixed-wing" from "fixed-wing_aircraft_and_airplane"). It is derived once in
// init() and never modified after that.
var firstTokenSet map[string]struct{}

// humanLabelsByLength groups the human sound classes in classes by label length.
// IsHumanVocalization uses the buckets for allocation-free, case-insensitive matching
// while the classifier scans a full prediction set for labels that must survive top-K.
var humanLabelsByLength map[int][]string

func init() {
	firstTokenSet = make(map[string]struct{})
	humanLabelsByLength = make(map[int][]string)
	for k, category := range classes {
		if before, _, found := strings.Cut(k, "_"); found {
			firstTokenSet[before] = struct{}{}
		}
		if category == CategoryHuman {
			humanLabelsByLength[len(k)] = append(humanLabelsByLength[len(k)], k)
		}
	}
}

// Categories returns all non-bird categories in a stable order.
func Categories() []Category {
	return []Category{
		CategoryHuman, CategoryAnimal, CategoryMusic, CategoryMechanical,
		CategoryEnvironment, CategoryNoise, CategoryDevice,
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

// IsHumanVocalization reports whether a full raw model label represents a human
// sound that should engage the privacy filter. It covers BirdNET's locale-stable
// "Human " label prefix, Perch's human AudioSet/FSD50K classes, and the Perch
// iNaturalist human taxon. Matching is case-insensitive.
func IsHumanVocalization(rawLabel string) bool {
	if hasFoldedPrefix(rawLabel, humanLabelPrefix) {
		return true
	}
	if strings.EqualFold(rawLabel, humanTaxonLabel) {
		return true
	}
	for _, label := range humanLabelsByLength[len(rawLabel)] {
		if strings.EqualFold(rawLabel, label) {
			return true
		}
	}
	return false
}

// IsDogDetection reports whether a full raw model label represents a domestic
// dog sound that should engage the dog-bark filter. It covers BirdNET's
// locale-stable "Dog_" prefix and Perch's dog sound classes and domestic dog
// taxon. Wild canids remain excluded. Matching is case-insensitive.
func IsDogDetection(rawLabel string) bool {
	if hasFoldedPrefix(rawLabel, dogLabelPrefix) {
		return true
	}
	return strings.EqualFold(rawLabel, dogLabel) ||
		strings.EqualFold(rawLabel, dogBarkLabel) ||
		strings.EqualFold(rawLabel, dogGrowlingLabel) ||
		strings.EqualFold(rawLabel, dogTaxonLabel)
}

func hasFoldedPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix)
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
