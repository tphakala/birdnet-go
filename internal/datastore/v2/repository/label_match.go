package repository

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/labels/soundevent"
)

// scientificNameLikeEscaper escapes the LIKE metacharacters %, _, and the escape
// character itself in a user-supplied scientific name, using '!' as the escape
// character. '!' is not special in any SQL dialect's string literals, so the
// generated SQL is identical and valid on MySQL, SQLite, and Postgres. A backslash
// escape ('\') must NOT be used: MySQL's default sql_mode treats a lone backslash
// in a string literal as an escape character, so "ESCAPE '\'" swallows the closing
// quote and raises a syntax error (Error 1064). SQLite does not treat backslash as
// special, which is why that only broke MySQL.
//
// It is a package-level value because strings.Replacer precomputes its matcher and
// is safe for concurrent use, so there is no need to rebuild it on every call.
var scientificNameLikeEscaper = strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`)

// LabelMatchForScientificName returns a predicate on an unqualified labels
// scientific_name column, and its arguments, selecting the labels that belong to
// scientificName in a history lookup.
//
// A bare scientific name matches exactly, or a legacy concatenated label stored
// as "ScientificName_CommonName". The "!_%" suffix is "literal underscore
// separator, then anything" ('!_' is an escaped underscore, '%' is the
// wildcard), mirroring how such labels are split on the first underscore (see
// detection.ExtractScientificName). A class name that is also the first token of
// a longer class matches exactly, because otherwise "Car" would also match the
// distinct class "Car_passing_by"; the legacy concatenated form of such a name
// is no longer matched. Every other name, including a single-token class like
// "Dog" that BirdNET also emits as "Dog_Dog", keeps the prefix match.
func LabelMatchForScientificName(scientificName string) (predicate string, args []any) {
	if soundevent.IsMultiWordClassPrefix(scientificName) {
		return "scientific_name = ?", []any{scientificName}
	}
	// Escape LIKE metacharacters with '!' (see scientificNameLikeEscaper).
	escaped := scientificNameLikeEscaper.Replace(scientificName)
	return "scientific_name = ? OR scientific_name LIKE ? ESCAPE '!'", []any{scientificName, escaped + `!_%`}
}
