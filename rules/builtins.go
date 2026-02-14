//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// MinMaxBuiltin detects manual min/max implementations using if statements
// or ternary-like patterns and suggests using the built-in min/max functions.
//
// Old patterns:
//
//	// If-based min
//	if a < b {
//	    result = a
//	} else {
//	    result = b
//	}
//
//	// math.Min/Max for integers
//	result := int(math.Min(float64(a), float64(b)))
//
// New pattern (Go 1.21+):
//
//	result := min(a, b)
//	result := max(a, b)
//
// Benefits:
//   - Cleaner, more readable code
//   - Works with any ordered type
//   - No type conversion needed
//
// See: https://pkg.go.dev/builtin#min
// See: https://pkg.go.dev/builtin#max
func MinMaxBuiltin(m dsl.Matcher) {
	// math.Min with float64 conversion for integers
	m.Match(
		`int(math.Min(float64($a), float64($b)))`,
	).
		Report("use min($a, $b) instead of int(math.Min(float64(...))) (Go 1.21+)").
		Suggest("min($a, $b)")

	m.Match(
		`int64(math.Min(float64($a), float64($b)))`,
	).
		Report("use min($a, $b) instead of int64(math.Min(float64(...))) (Go 1.21+)").
		Suggest("min($a, $b)")

	m.Match(
		`int32(math.Min(float64($a), float64($b)))`,
	).
		Report("use min($a, $b) instead of int32(math.Min(float64(...))) (Go 1.21+)").
		Suggest("min($a, $b)")

	// math.Max with float64 conversion for integers
	m.Match(
		`int(math.Max(float64($a), float64($b)))`,
	).
		Report("use max($a, $b) instead of int(math.Max(float64(...))) (Go 1.21+)").
		Suggest("max($a, $b)")

	m.Match(
		`int64(math.Max(float64($a), float64($b)))`,
	).
		Report("use max($a, $b) instead of int64(math.Max(float64(...))) (Go 1.21+)").
		Suggest("max($a, $b)")

	m.Match(
		`int32(math.Max(float64($a), float64($b)))`,
	).
		Report("use max($a, $b) instead of int32(math.Max(float64(...))) (Go 1.21+)").
		Suggest("max($a, $b)")
}

// ClearBuiltin detects loop-based map/slice clearing patterns and suggests
// using the built-in clear() function.
//
// Old patterns:
//
//	// Map clearing
//	for k := range m {
//	    delete(m, k)
//	}
//
//	// Slice zeroing
//	for i := range s {
//	    s[i] = 0  // or nil, "", etc.
//	}
//
// New pattern (Go 1.21+):
//
//	clear(m)  // Deletes all map entries
//	clear(s)  // Sets all slice elements to zero value
//
// Benefits:
//   - Cleaner, more readable code
//   - More efficient (optimized implementation)
//   - Works with maps and slices
//
// See: https://pkg.go.dev/builtin#clear
func ClearBuiltin(m dsl.Matcher) {
	// Map clearing pattern: for k := range m { delete(m, k) }
	m.Match(
		`for $k := range $m { delete($m, $k) }`,
	).
		Report("use clear($m) instead of loop-based map clearing (Go 1.21+)").
		Suggest("clear($m)")

	// Map clearing with underscore value: for k, _ := range m { delete(m, k) }
	m.Match(
		`for $k, _ := range $m { delete($m, $k) }`,
	).
		Report("use clear($m) instead of loop-based map clearing (Go 1.21+)").
		Suggest("clear($m)")
}

// RangeOverInteger detects traditional for loops that iterate from 0 to n
// and suggests using the Go 1.22+ range-over-integer syntax.
//
// Old pattern:
//
//	for i := 0; i < n; i++ {
//	    process(i)
//	}
//
// New pattern (Go 1.22+):
//
//	for i := range n {
//	    process(i)
//	}
//
// Benefits:
//   - More concise and readable
//   - Intent is clearer (iterate n times)
//   - Less error-prone (no off-by-one mistakes)
//
// Note: Only matches loops starting from 0 with < comparison and i++.
// Loops with different starting values, comparisons, or increments
// are intentionally not flagged.
//
// See: https://go.dev/doc/go1.22#language
func RangeOverInteger(m dsl.Matcher) {
	// Pattern: for i := 0; i < n; i++
	// Exclude benchmark loops (b.N) which should use b.Loop() instead
	m.Match(
		`for $i := 0; $i < $n; $i++ { $*body }`,
	).
		Where(
			!m["n"].Text.Matches(`.*\.N$`) &&
				!m["n"].Text.Matches(`\.(NumField|NumMethod|NumIn|NumOut)\(\)$`),
		).
		Report("use for $i := range $n instead of for $i := 0; $i < $n; $i++ (Go 1.22+)").
		Suggest("for $i := range $n { $body }")
}

// AppendWithoutValues detects append calls with no values which have no effect.
//
// Broken pattern:
//
//	slice = append(slice)  // No effect
//
// See: https://pkg.go.dev/builtin#append
// Note: Go 1.22 vet tool also warns about this pattern.
func AppendWithoutValues(m dsl.Matcher) {
	m.Match(
		`append($s)`,
	).
		Report("append with single argument has no effect; did you forget the values to append?")
}

// NewWithExpression detects the slice-literal hack for getting a pointer to a value
// and suggests using Go 1.26's enhanced new() built-in.
//
// Old pattern (slice hack):
//
//	field := &[]string{"hello"}[0]
//	field := &[]int{42}[0]
//	field := &[]time.Duration{5 * time.Second}[0]
//
// New pattern (Go 1.26+):
//
//	field := new("hello")
//	field := new(42)
//	field := new(5 * time.Second)
//
// Benefits:
//   - Eliminates the obscure slice-literal-index hack
//   - Clearer intent: "pointer to this value"
//   - No intermediate slice allocation
//   - Works with any expression, including function calls
//
// See: https://go.dev/doc/go1.26#language
func NewWithExpression(m dsl.Matcher) {
	// Pattern: &[]T{v}[0] - the well-known slice hack for pointer-to-value
	m.Match(
		`&[]$typ{$val}[0]`,
	).
		Report("use new($val) instead of &[]$typ{$val}[0] (Go 1.26+)").
		Suggest("new($val)")
}
