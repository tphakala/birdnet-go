package speciesindex

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Fold normalizes a species name for case-insensitive lookup: NFC normalization
// followed by lower-casing. It is the single fold used for every species-name
// search key in the process, so composed and decomposed accents (for example a
// precomposed "é" and an "e" plus combining acute) collapse to the same key and
// forward display and reverse search never disagree on normalization.
//
// This is the exact expression both legacy name-map builders used
// (strings.ToLower(norm.NFC.String(s))); the datastore inlined it and api/v2
// reached it through apicore.NormalizeForLookup, which now delegates here.
func Fold(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}
