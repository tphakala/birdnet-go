//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// ReflectTypeAssert detects v.Interface().(T) pattern and suggests reflect.TypeAssert.
//
// The old pattern (allocates):
//
//	val := v.Interface().(string)
//	val, ok := v.Interface().(string)
//
// New pattern (Go 1.25+, no allocation):
//
//	val := reflect.TypeAssert[string](v)
//	val, ok := reflect.TypeAssert[string](v)
//
// reflect.TypeAssert converts Value directly to typed value without intermediate
// allocation via Interface(). This is more efficient for hot paths.
//
// See: https://pkg.go.dev/reflect#TypeAssert
func ReflectTypeAssert(m dsl.Matcher) {
	// Pattern: Type assertion on Interface() result
	m.Match(
		`$v.Interface().($typ)`,
	).
		Where(m["v"].Type.Is("reflect.Value")).
		Report("use reflect.TypeAssert[$typ]($v) instead of $v.Interface().($typ) to avoid allocation (Go 1.25+)")
}

// ReflectPtrTo detects deprecated reflect.PtrTo and suggests reflect.PointerTo.
//
// Deprecated pattern:
//
//	ptrType := reflect.PtrTo(t)
//
// New pattern (Go 1.22+):
//
//	ptrType := reflect.PointerTo(t)
//
// reflect.PtrTo was deprecated in Go 1.22 in favor of the clearer name PointerTo.
//
// See: https://pkg.go.dev/reflect#PointerTo
func ReflectPtrTo(m dsl.Matcher) {
	m.Match(
		`reflect.PtrTo($t)`,
	).
		Report("reflect.PtrTo is deprecated in Go 1.22; use reflect.PointerTo($t) instead").
		Suggest("reflect.PointerTo($t)")
}

// ReflectTypeOf detects the common pattern of getting a reflect.Type via TypeOf
// with a nil pointer and suggests using the cleaner reflect.TypeFor generic.
//
// Old pattern:
//
//	t := reflect.TypeOf((*MyType)(nil)).Elem()
//
// New pattern (Go 1.22+):
//
//	t := reflect.TypeFor[MyType]()
//
// Benefits:
//   - More readable and concise
//   - No need for nil pointer cast trick
//   - Type is checked at compile time
//
// See: https://pkg.go.dev/reflect#TypeFor
func ReflectTypeOf(m dsl.Matcher) {
	m.Match(
		`reflect.TypeOf((*$typ)(nil)).Elem()`,
	).
		Report("use reflect.TypeFor[$typ]() instead of reflect.TypeOf((*$typ)(nil)).Elem() (Go 1.22+)")
}

// DeprecatedReflectHeaders detects deprecated reflect.SliceHeader and
// reflect.StringHeader usage and suggests using unsafe.Slice/unsafe.String.
//
// Deprecated patterns:
//
//	sh := (*reflect.SliceHeader)(unsafe.Pointer(&slice))
//	hdr := (*reflect.StringHeader)(unsafe.Pointer(&str))
//
// New patterns (Go 1.21+):
//
//	// For creating slices from pointers:
//	slice := unsafe.Slice(ptr, len)
//
//	// For creating strings from pointers:
//	str := unsafe.String(ptr, len)
//
// Benefits:
//   - Type-safe
//   - No need for manual header manipulation
//   - Less error-prone
//
// See: https://pkg.go.dev/unsafe#Slice
// See: https://pkg.go.dev/unsafe#String
func DeprecatedReflectHeaders(m dsl.Matcher) {
	m.Match(
		`reflect.SliceHeader{}`,
		`reflect.SliceHeader{$*_}`,
	).
		Report("reflect.SliceHeader is deprecated in Go 1.21; use unsafe.Slice instead")

	m.Match(
		`reflect.StringHeader{}`,
		`reflect.StringHeader{$*_}`,
	).
		Report("reflect.StringHeader is deprecated in Go 1.21; use unsafe.String instead")

	// Casting to SliceHeader
	m.Match(
		`(*reflect.SliceHeader)($x)`,
	).
		Report("reflect.SliceHeader is deprecated in Go 1.21; use unsafe.Slice instead")

	// Casting to StringHeader
	m.Match(
		`(*reflect.StringHeader)($x)`,
	).
		Report("reflect.StringHeader is deprecated in Go 1.21; use unsafe.String instead")
}
