//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// ErrorsAsType detects errors.As with a pointer target and suggests errors.AsType.
//
// The old pattern:
//
//	var pathErr *fs.PathError
//	if errors.As(err, &pathErr) {
//	    fmt.Println(pathErr.Path)
//	}
//
// New pattern (Go 1.26+):
//
//	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
//	    fmt.Println(pathErr.Path)
//	}
//
// Benefits:
//   - Type-safe: checked at compile time, no risk of passing wrong pointer type
//   - Faster: avoids reflection internally
//   - Reduces LOC: no separate variable declaration needed
//   - Scopes the variable to the if block
//
// The rule resolves errors to internal/errors, which every package in this
// module uses instead of the standard library (depguard bans the latter).
// Targets whose type does not implement error (for example an anonymous
// interface such as interface{ Timeout() bool }) are skipped, since AsType
// requires E to satisfy error.
//
// See: internal/errors/errors.go AsType, a passthrough to the standard AsType.
func ErrorsAsType(m dsl.Matcher) {
	m.Import("github.com/tphakala/birdnet-go/internal/errors")

	// Pattern: errors.As(err, &target)
	m.Match(
		`errors.As($err, &$target)`,
	).
		Where(m["target"].Type.Implements("error")).
		Report("use errors.AsType[T]($err) instead of errors.As($err, &$target) for a type-safe, reflection-free error assertion")
}
