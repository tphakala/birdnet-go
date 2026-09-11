package speciesindex

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Fold normalizes a species name for case-insensitive lookup: NFC normalization
// followed by lower-casing. It is the canonical fold for species-name search
// keys: the speciesindex name maps, apicore.NormalizeForLookup and the datastore
// name maps all route through it, so composed and decomposed accents (for example
// a precomposed "é" and an "e" plus combining acute) collapse to the same key and
// forward display and reverse search agree on normalization. Any new search-key
// site should call Fold rather than re-inline the expression.
//
// This is the exact expression both legacy name-map builders used
// (strings.ToLower(norm.NFC.String(s))); the datastore inlined it and api/v2
// reached it through apicore.NormalizeForLookup, which now delegates here.
func Fold(s string) string {
	return strings.ToLower(norm.NFC.String(s))
}
