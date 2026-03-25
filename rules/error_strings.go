//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// BrittleErrorStringMatch detects strings.Contains(err.Error(), ...) patterns
// in production code and suggests using typed errors instead.
//
// The brittle pattern:
//
//	if strings.Contains(err.Error(), "database is locked") {
//	    // retry
//	}
//
// Better pattern:
//
//	var sqliteErr *sqlite3.Error
//	if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrBusy {
//	    // retry
//	}
//
// Why this matters:
//   - Error message text can change between library versions
//   - Messages may differ across database drivers or locales
//   - errors.Is/errors.As are the Go-idiomatic way to classify errors
//   - String matching silently breaks when upstream changes wording
//
// This rule only fires in production code (not test files), since
// string matching in tests is acceptable for asserting error messages.
//
// False positive guidance:
//   - Some errors (e.g., from exec.Command, net, certain DB drivers)
//     genuinely lack typed error values. In those cases, add a comment
//     explaining why string matching is necessary and use //nolint:gocritic.
func BrittleErrorStringMatch(m dsl.Matcher) {
	const validationNote = ". VALIDATE: check if the library/driver exposes a typed error or error code before fixing. " +
		"If no typed error exists, suppress with //nolint:gocritic and a comment explaining why string matching is the only option"

	// Pattern 1: strings.Contains(err.Error(), "...")
	m.Match(
		`strings.Contains($err.Error(), $msg)`,
	).
		Where(!m.File().Name.Matches(`_test\.go$`)).
		Report("brittle error classification: strings.Contains($err.Error(), $msg) breaks if message text changes; " +
			"prefer errors.Is(), errors.As(), or typed error codes" + validationNote)

	// Pattern 2: strings.Contains(strings.ToLower(err.Error()), "...")
	m.Match(
		`strings.Contains(strings.ToLower($err.Error()), $msg)`,
	).
		Where(!m.File().Name.Matches(`_test\.go$`)).
		Report("brittle error classification: string matching on lowercased error text is fragile; " +
			"prefer errors.Is(), errors.As(), or typed error codes" + validationNote)

	// Pattern 3: strings.HasPrefix/HasSuffix on err.Error()
	m.Match(
		`strings.HasPrefix($err.Error(), $msg)`,
	).
		Where(!m.File().Name.Matches(`_test\.go$`)).
		Report("brittle error classification: strings.HasPrefix on error text is fragile; " +
			"prefer errors.Is(), errors.As(), or typed error codes" + validationNote)

	m.Match(
		`strings.HasSuffix($err.Error(), $msg)`,
	).
		Where(!m.File().Name.Matches(`_test\.go$`)).
		Report("brittle error classification: strings.HasSuffix on error text is fragile; " +
			"prefer errors.Is(), errors.As(), or typed error codes" + validationNote)
}
