package soundevent

// birdnetClasses holds the non-species classes of the BirdNET models,
// lowercased: the eleven classes that appear in every BirdNET_GLOBAL_6K_V2.4
// label file (the English scientific half of the localized "Name_Localized"
// labels, identical in every locale), the legacy "jet" class from older model
// outputs, and the sixteen "X_X" classes that end the BirdNET+ V3.0 label files
// (four of which, engine, fireworks, gun and siren, are also V2.4 classes and
// are listed once). Keys are matched case-insensitively against the part of a
// raw label before the first underscore, or against a stored class name. It is
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

	// BirdNET+ V3.0 classes not already present above.
	"aircraft":      {},
	"boat":          {},
	"chainsaw":      {},
	"explosion":     {},
	"fire":          {},
	"hammer":        {},
	"motor vehicle": {},
	"music":         {},
	"power tool":    {},
	"sawing":        {},
	"train":         {},
	"vehicle horn":  {},
}
