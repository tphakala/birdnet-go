//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// SortInts detects sort.Ints/sort.Strings/sort.Float64s and suggests slices.Sort.
//
// Old patterns:
//
//	sort.Ints(nums)
//	sort.Strings(strs)
//	sort.Float64s(floats)
//
// New pattern (Go 1.21+):
//
//	slices.Sort(nums)
//	slices.Sort(strs)
//	slices.Sort(floats)
//
// Benefits:
//   - Generic, works with any ordered slice
//   - Consistent API across types
//   - Part of the new slices package
//
// See: https://pkg.go.dev/slices#Sort
func SortInts(m dsl.Matcher) {
	m.Match(
		`sort.Ints($s)`,
	).
		Report("use slices.Sort($s) instead of sort.Ints (Go 1.21+)").
		Suggest("slices.Sort($s)")

	m.Match(
		`sort.Strings($s)`,
	).
		Report("use slices.Sort($s) instead of sort.Strings (Go 1.21+)").
		Suggest("slices.Sort($s)")

	m.Match(
		`sort.Float64s($s)`,
	).
		Report("use slices.Sort($s) instead of sort.Float64s (Go 1.21+)").
		Suggest("slices.Sort($s)")

	// sort.IntsAreSorted, etc.
	m.Match(
		`sort.IntsAreSorted($s)`,
	).
		Report("use slices.IsSorted($s) instead of sort.IntsAreSorted (Go 1.21+)").
		Suggest("slices.IsSorted($s)")

	m.Match(
		`sort.StringsAreSorted($s)`,
	).
		Report("use slices.IsSorted($s) instead of sort.StringsAreSorted (Go 1.21+)").
		Suggest("slices.IsSorted($s)")

	m.Match(
		`sort.Float64sAreSorted($s)`,
	).
		Report("use slices.IsSorted($s) instead of sort.Float64sAreSorted (Go 1.21+)").
		Suggest("slices.IsSorted($s)")
}

// BytesClone detects manual byte slice cloning and suggests bytes.Clone.
//
// Old patterns:
//
//	clone := make([]byte, len(original))
//	copy(clone, original)
//
//	clone := append([]byte(nil), original...)
//	clone := append([]byte{}, original...)
//
// New pattern (Go 1.20+):
//
//	clone := bytes.Clone(original)
//
// Benefits:
//   - More readable
//   - Less error-prone
//   - Single function call
//
// See: https://pkg.go.dev/bytes#Clone
func BytesClone(m dsl.Matcher) {
	// Pattern: append([]byte(nil), b...)
	m.Match(
		`append([]byte(nil), $b...)`,
	).
		Report("use bytes.Clone($b) instead of append([]byte(nil), $b...) (Go 1.20+)")

	// Pattern: append([]byte{}, b...)
	m.Match(
		`append([]byte{}, $b...)`,
	).
		Report("use bytes.Clone($b) instead of append([]byte{}, $b...) (Go 1.20+)")

	// Pattern: append(b[:0:0], b...)
	m.Match(
		`append($b[:0:0], $b...)`,
	).
		Where(m["b"].Type.Is("[]byte")).
		Report("use bytes.Clone($b) instead of append($b[:0:0], $b...) (Go 1.20+)")
}

// SlicesClone detects manual slice cloning patterns and suggests slices.Clone.
//
// Old patterns:
//
//	clone := make([]T, len(original))
//	copy(clone, original)
//
//	clone := append([]T(nil), original...)
//
// New pattern (Go 1.21+):
//
//	clone := slices.Clone(original)
//
// Benefits:
//   - More readable
//   - Less error-prone
//   - Single function call
//
// See: https://pkg.go.dev/slices#Clone
func SlicesClone(m dsl.Matcher) {
	// Pattern: append([]T(nil), s...)
	// This is a common idiom for cloning slices
	m.Match(
		`append([]$typ(nil), $s...)`,
	).
		Report("use slices.Clone($s) instead of append([]$typ(nil), $s...) (Go 1.21+)")

	m.Match(
		`append([]$typ{}, $s...)`,
	).
		Report("use slices.Clone($s) instead of append([]$typ{}, $s...) (Go 1.21+)")

	// append(s[:0:0], s...) pattern
	m.Match(
		`append($s[:0:0], $s...)`,
	).
		Report("use slices.Clone($s) instead of append($s[:0:0], $s...) (Go 1.21+)")
}

// BackwardIteration detects manual reverse iteration patterns and suggests slices.Backward.
//
// Old pattern:
//
//	for i := len(s) - 1; i >= 0; i-- {
//	    process(s[i])
//	}
//
// New pattern (Go 1.23+):
//
//	for i, v := range slices.Backward(s) {
//	    process(v)
//	}
//
// Benefits:
//   - Clearer intent
//   - Less error-prone (off-by-one errors)
//   - Works with iterator composition
//
// See: https://pkg.go.dev/slices#Backward
func BackwardIteration(m dsl.Matcher) {
	// Pattern: for i := len(s) - 1; i >= 0; i--
	m.Match(
		`for $i := len($s) - 1; $i >= 0; $i-- { $*body }`,
	).
		Report("use slices.Backward($s) for reverse iteration (Go 1.23+)")

	// Pattern: for i := len(s) - 1; i > -1; i--
	m.Match(
		`for $i := len($s) - 1; $i > -1; $i-- { $*body }`,
	).
		Report("use slices.Backward($s) for reverse iteration (Go 1.23+)")
}

// MapKeysCollection detects manual map key collection patterns and suggests maps.Keys.
//
// Old pattern:
//
//	keys := make([]string, 0, len(m))
//	for k := range m {
//	    keys = append(keys, k)
//	}
//
// New pattern (Go 1.23+):
//
//	keys := slices.Collect(maps.Keys(m))
//
// Benefits:
//   - More concise and readable
//   - Works with iterator composition
//   - Can be sorted directly: slices.Sorted(maps.Keys(m))
//
// See: https://pkg.go.dev/maps#Keys
// See: https://pkg.go.dev/slices#Collect
func MapKeysCollection(m dsl.Matcher) {
	// Pattern: for k := range m { keys = append(keys, k) }
	// This is a common pattern for collecting map keys
	// Type guard ensures we only match maps, not channels or iterators
	m.Match(
		`for $k := range $m { $keys = append($keys, $k) }`,
	).
		Where(m["m"].Type.Is("map[$k]$v")).
		Report("use slices.Collect(maps.Keys($m)) to collect map keys (Go 1.23+)")

	// Pattern with underscore for value: for k, _ := range m
	m.Match(
		`for $k, _ := range $m { $keys = append($keys, $k) }`,
	).
		Where(m["m"].Type.Is("map[$k]$v")).
		Report("use slices.Collect(maps.Keys($m)) to collect map keys (Go 1.23+)")
}

// MapValuesCollection detects manual map value collection patterns and suggests maps.Values.
//
// Old pattern:
//
//	values := make([]V, 0, len(m))
//	for _, v := range m {
//	    values = append(values, v)
//	}
//
// New pattern (Go 1.23+):
//
//	values := slices.Collect(maps.Values(m))
//
// See: https://pkg.go.dev/maps#Values
// See: https://pkg.go.dev/slices#Collect
func MapValuesCollection(m dsl.Matcher) {
	// Pattern: for _, v := range m { values = append(values, v) }
	// Type guard ensures we only match maps, not slices or other iterables
	m.Match(
		`for _, $v := range $m { $values = append($values, $v) }`,
	).
		Where(m["m"].Type.Is("map[$k]$v")).
		Report("use slices.Collect(maps.Values($m)) to collect map values (Go 1.23+)")
}

// SliceRepeat detects manual slice repetition patterns and suggests slices.Repeat.
//
// Old pattern:
//
//	result := make([]T, 0, len(s)*n)
//	for i := 0; i < n; i++ {
//	    result = append(result, s...)
//	}
//
// New pattern (Go 1.23+):
//
//	result := slices.Repeat(s, n)
//
// See: https://pkg.go.dev/slices#Repeat
func SliceRepeat(m dsl.Matcher) {
	// Pattern: for loop appending same slice multiple times
	m.Match(
		`for $i := 0; $i < $n; $i++ { $result = append($result, $s...) }`,
	).
		Report("use slices.Repeat($s, $n) instead of manual repetition loop (Go 1.23+); false positive if $s depends on the loop variable")

	// Pattern: range-over-integer form (with variable)
	m.Match(
		`for $i := range $n { $result = append($result, $s...) }`,
	).
		Report("use slices.Repeat($s, $n) instead of manual repetition loop (Go 1.23+); false positive if $s depends on the loop variable")

	// Pattern: range-over-integer form (without variable)
	m.Match(
		`for range $n { $result = append($result, $s...) }`,
	).
		Report("use slices.Repeat($s, $n) instead of manual repetition loop (Go 1.23+)")
}
