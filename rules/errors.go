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
// See: https://pkg.go.dev/errors#AsType
func ErrorsAsType(m dsl.Matcher) {
	// Pattern: errors.As(err, &target)
	// This catches all errors.As calls with address-of second argument
	m.Match(
		`errors.As($err, &$target)`,
	).
		Report("use errors.AsType[$target]($err) instead of errors.As for type-safe, faster error assertion (Go 1.26+)")
}
