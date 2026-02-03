//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// ErrorsNewf detects errors.New(fmt.Errorf(...)) and suggests errors.Newf.
//
// Old pattern:
//
//	errors.New(
//		fmt.Errorf("something failed: %w", err)).
//		Component("foo")
//
// New pattern:
//
//	errors.Newf("something failed: %w", err).
//		Component("foo")
//
// Benefits:
//   - Less nesting, more readable
//   - Consistent use of the internal errors API
//
// See: internal/errors/errors.go — Newf is a shorthand for New(fmt.Errorf(...))
func ErrorsNewf(m dsl.Matcher) {
	m.Import("github.com/tphakala/birdnet-go/internal/errors")

	// Without format args: errors.New(fmt.Errorf("literal string"))
	// Must be listed before the variadic pattern to avoid trailing comma in Suggest.
	m.Match(
		`errors.New(fmt.Errorf($format))`,
	).
		Report("use errors.Newf($format) instead of errors.New(fmt.Errorf(...))").
		Suggest("errors.Newf($format)")

	// With format args: errors.New(fmt.Errorf("msg: %v", val))
	m.Match(
		`errors.New(fmt.Errorf($format, $*args))`,
	).
		Report("use errors.Newf($format, $args) instead of errors.New(fmt.Errorf(...))").
		Suggest("errors.Newf($format, $args)")
}
