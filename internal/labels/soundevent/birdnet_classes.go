package soundevent

// birdnetClasses holds the non-species classes of the BirdNET v2.x models,
// lowercased: the eleven classes that appear in every BirdNET_GLOBAL_6K_V2.4
// label file (the English scientific half of the localized "Name_Localized"
// labels, identical in every locale) plus the legacy "jet" class from older
// model outputs. Keys are matched case-insensitively against the part of a raw
// label before the first underscore, or against a stored class name. It is
// never modified after package initialization.
var birdnetClasses = map[string]struct{}{
	"dog":             {},
	"engine":          {},
	"environmental":   {},
	"fireworks":       {},
	"gun":             {},
	"human non-vocal": {},
	"human vocal":     {},
	"human whistle":   {},
	"jet":             {},
	"noise":           {},
	"power tools":     {},
	"siren":           {},
}
