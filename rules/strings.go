//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// StringsLinesIteration detects manual line splitting patterns and suggests strings.Lines.
//
// Old pattern:
//
//	for _, line := range strings.Split(s, "\n") {
//	    process(line)
//	}
//
// New pattern (Go 1.23+):
//
//	for line := range strings.Lines(s) {
//	    process(line)
//	}
//
// Benefits:
//   - No intermediate slice allocation
//   - Handles both \n and \r\n line endings
//   - More memory efficient for large strings
//
// See: https://pkg.go.dev/strings#Lines
// See: https://pkg.go.dev/bytes#Lines
func StringsLinesIteration(m dsl.Matcher) {
	// Pattern: for _, line := range strings.Split(s, "\n")
	m.Match(
		`for $_, $line := range strings.Split($s, "\n") { $*body }`,
	).
		Report(`use for $line := range strings.Lines($s) instead of ranging over strings.Split($s, "\n") (Go 1.23+); note: Lines() handles both \n and \r\n`)

	// Pattern: for _, line := range strings.Split(s, "\r\n")
	m.Match(
		`for $_, $line := range strings.Split($s, "\r\n") { $*body }`,
	).
		Report(`use for $line := range strings.Lines($s) instead of ranging over strings.Split($s, "\r\n") (Go 1.23+)`)

	// Also detect bytes.Split for line iteration
	m.Match(
		`for $_, $line := range bytes.Split($s, []byte("\n")) { $*body }`,
	).
		Report(`use for $line := range bytes.Lines($s) instead of ranging over bytes.Split($s, []byte("\n")) (Go 1.23+)`)

	m.Match(
		`for $_, $line := range bytes.Split($s, []byte{'\n'}) { $*body }`,
	).
		Report(`use for $line := range bytes.Lines($s) instead of ranging over bytes.Split (Go 1.23+)`)
}

// StringsSplitIteration detects strings.Split used only for iteration
// and suggests strings.SplitSeq for better memory efficiency.
//
// Old pattern:
//
//	for _, part := range strings.Split(s, ",") {
//	    process(part)
//	}
//
// New pattern (Go 1.23+):
//
//	for part := range strings.SplitSeq(s, ",") {
//	    process(part)
//	}
//
// Benefits:
//   - No intermediate slice allocation
//   - Better for large strings with many parts
//   - Works with iterator composition
//
// Note: Only use SplitSeq when you're just iterating. If you need the slice
// result (e.g., to access by index or get length), keep using Split.
//
// See: https://pkg.go.dev/strings#SplitSeq
// See: https://pkg.go.dev/bytes#SplitSeq
func StringsSplitIteration(m dsl.Matcher) {
	// Pattern: for _, part := range strings.Split(s, sep)
	// Excluding newline separators which should use Lines() instead
	m.Match(
		`for $_, $part := range strings.Split($s, $sep) { $*body }`,
	).
		Where(!m["sep"].Text.Matches(`^"\\n"$`) && !m["sep"].Text.Matches(`^"\\r\\n"$`)).
		Report("use for $part := range strings.SplitSeq($s, $sep) to avoid intermediate slice allocation (Go 1.23+)")

	// bytes.Split pattern
	m.Match(
		`for $_, $part := range bytes.Split($s, $sep) { $*body }`,
	).
		Where(!m["sep"].Text.Matches(`\[\]byte\("\\n"\)`) && !m["sep"].Text.Matches(`\[\]byte\{.*\\n.*\}`)).
		Report("use for $part := range bytes.SplitSeq($s, $sep) to avoid intermediate slice allocation (Go 1.23+)")
}

// StringsFieldsIteration detects strings.Fields used only for iteration
// and suggests strings.FieldsSeq.
//
// Old pattern:
//
//	for _, field := range strings.Fields(s) {
//	    process(field)
//	}
//
// New pattern (Go 1.23+):
//
//	for field := range strings.FieldsSeq(s) {
//	    process(field)
//	}
//
// See: https://pkg.go.dev/strings#FieldsSeq
// See: https://pkg.go.dev/bytes#FieldsSeq
func StringsFieldsIteration(m dsl.Matcher) {
	m.Match(
		`for $_, $field := range strings.Fields($s) { $*body }`,
	).
		Report("use for $field := range strings.FieldsSeq($s) to avoid intermediate slice allocation (Go 1.23+)")

	m.Match(
		`for $_, $field := range bytes.Fields($s) { $*body }`,
	).
		Report("use for $field := range bytes.FieldsSeq($s) to avoid intermediate slice allocation (Go 1.23+)")
}

// StringsFieldsFuncIteration detects strings.FieldsFunc used only for iteration
// and suggests strings.FieldsFuncSeq.
//
// Old pattern:
//
//	for _, field := range strings.FieldsFunc(s, f) {
//	    process(field)
//	}
//
// New pattern (Go 1.23+):
//
//	for field := range strings.FieldsFuncSeq(s, f) {
//	    process(field)
//	}
//
// See: https://pkg.go.dev/strings#FieldsFuncSeq
// See: https://pkg.go.dev/bytes#FieldsFuncSeq
func StringsFieldsFuncIteration(m dsl.Matcher) {
	m.Match(
		`for $_, $field := range strings.FieldsFunc($s, $f) { $*body }`,
	).
		Report("use for $field := range strings.FieldsFuncSeq($s, $f) to avoid intermediate slice allocation (Go 1.23+)")

	m.Match(
		`for $_, $field := range bytes.FieldsFunc($s, $f) { $*body }`,
	).
		Report("use for $field := range bytes.FieldsFuncSeq($s, $f) to avoid intermediate slice allocation (Go 1.23+)")
}
