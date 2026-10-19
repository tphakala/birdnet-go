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

const (
	// labelWordSeparator joins the words of a multi-word model label.
	labelWordSeparator = "_"
	// displayWordSeparator joins the words of a human-readable class name.
	displayWordSeparator = " "
)

// firstTokenSet holds the first underscore-delimited token of every multi-word key in classes.
// Keys are split on the first "_" only, so a token may itself contain a hyphen
// (e.g. "fixed-wing" from "fixed-wing_aircraft_and_airplane"). It is derived once in
// init() and never modified after that.
var firstTokenSet map[string]struct{}

func init() {
	firstTokenSet = make(map[string]struct{})
	for k := range classes {
		if before, _, found := strings.Cut(k, labelWordSeparator); found {
			firstTokenSet[before] = struct{}{}
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

// IsNonSpeciesLabel reports whether rawLabel is a known non-bird sound class
// (the full-label exact-match path). Equivalent to: _, ok := CategoryOf(rawLabel); ok.
func IsNonSpeciesLabel(rawLabel string) bool {
	_, ok := CategoryOf(rawLabel)
	return ok
}

// MultiWordLabelNames recognises a full multi-word (underscore-joined) non-bird
// class label such as "Domestic_sounds_and_home_sounds". The match is an exact,
// case-insensitive whole-label lookup in the known class set, after trimming
// surrounding whitespace and dropping carriage returns. When it matches, identity
// is the sanitized label with the model's case and underscores kept, and display
// is the identity with underscores replaced by spaces. Single-token classes and
// unknown labels return ok=false: callers already keep a single-token label whole,
// and every other label follows the BirdNET "Scientific_Common" rule.
func MultiWordLabelNames(label string) (identity, display string, ok bool) {
	label = strings.TrimSpace(label)
	label = strings.ReplaceAll(label, "\r", "")
	if strings.IndexByte(label, labelWordSeparator[0]) < 0 {
		return "", "", false
	}
	if _, known := CategoryOf(label); !known {
		return "", "", false
	}
	return label, strings.ReplaceAll(label, labelWordSeparator, displayWordSeparator), true
}

// IsMultiWordClassPrefix reports whether name is a known non-bird class that is
// also the first token of a longer multi-word class (for example "car", which
// begins "car_passing_by"). Case-insensitive.
func IsMultiWordClassPrefix(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := classes[lower]; !ok {
		return false
	}
	_, ok := firstTokenSet[lower]
	return ok
}

// IsNonBirdName reports whether name is a non-bird class, matching EITHER the full
// label OR the first token of a multi-word (underscore-joined) class. New detections
// carry the full label; the first-token match remains for names stored before
// full labels were kept whole (e.g. "Power" from "power_tool", "Engine" from
// "engine"), and for the image provider's lookups of those names.
// Case-insensitive.
func IsNonBirdName(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := classes[lower]; ok {
		return true
	}
	_, ok := firstTokenSet[lower]
	return ok
}
