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

// ReflectFieldsIterator detects manual index-based iteration over struct fields
// and suggests using the iterator methods added in Go 1.26.
//
// Old pattern:
//
//	for i := 0; i < t.NumField(); i++ {
//	    f := t.Field(i)
//	    // use f
//	}
//
// New patterns (Go 1.26+):
//
//	for f := range t.Fields() {       // reflect.Type
//	    // use f
//	}
//	for sf, v := range val.Fields() { // reflect.Value
//	    // use sf (StructField) and v (Value)
//	}
//
// Benefits:
//   - Cleaner, more idiomatic Go iteration
//   - No off-by-one risk
//   - Consistent with Go 1.23+ iterator patterns
//   - Reduces boilerplate
//
// See: https://pkg.go.dev/reflect#Type.Fields
// See: https://pkg.go.dev/reflect#Value.Fields
func ReflectFieldsIterator(m dsl.Matcher) {
	// Type.NumField loop
	m.Match(
		`for $i := 0; $i < $t.NumField(); $i++ { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Fields() instead of index-based field iteration (Go 1.26+); if the loop index is also used for reflect.Value field access, range over the Value instead")

	// Value.NumField loop
	m.Match(
		`for $i := 0; $i < $v.NumField(); $i++ { $*_ }`,
	).
		Where(m["v"].Type.Is("reflect.Value")).
		Report("use range $v.Fields() instead of index-based field iteration (Go 1.26+)")

	// range over NumField integer (Go 1.22+ style)
	m.Match(
		`for $i := range $t.NumField() { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Fields() instead of range $t.NumField() (Go 1.26+); if the loop index is also used for reflect.Value field access, range over the Value instead")

	m.Match(
		`for $i := range $v.NumField() { $*_ }`,
	).
		Where(m["v"].Type.Is("reflect.Value")).
		Report("use range $v.Fields() instead of range $v.NumField() (Go 1.26+)")
}

// ReflectMethodsIterator detects manual index-based iteration over type methods
// and suggests using the iterator methods added in Go 1.26.
//
// Old pattern:
//
//	for i := 0; i < t.NumMethod(); i++ {
//	    m := t.Method(i)
//	    // use m
//	}
//
// New patterns (Go 1.26+):
//
//	for m := range t.Methods() {       // reflect.Type
//	    // use m
//	}
//	for m, v := range val.Methods() {  // reflect.Value
//	    // use m (Method) and v (Value)
//	}
//
// Benefits:
//   - Cleaner, more idiomatic Go iteration
//   - No off-by-one risk
//   - Consistent with Go 1.23+ iterator patterns
//
// See: https://pkg.go.dev/reflect#Type.Methods
// See: https://pkg.go.dev/reflect#Value.Methods
func ReflectMethodsIterator(m dsl.Matcher) {
	// Type.NumMethod loop
	m.Match(
		`for $i := 0; $i < $t.NumMethod(); $i++ { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Methods() instead of index-based method iteration (Go 1.26+)")

	// Value.NumMethod loop
	m.Match(
		`for $i := 0; $i < $v.NumMethod(); $i++ { $*_ }`,
	).
		Where(m["v"].Type.Is("reflect.Value")).
		Report("use range $v.Methods() instead of index-based method iteration (Go 1.26+)")

	// range over NumMethod integer
	m.Match(
		`for $i := range $t.NumMethod() { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Methods() instead of range $t.NumMethod() (Go 1.26+)")

	m.Match(
		`for $i := range $v.NumMethod() { $*_ }`,
	).
		Where(m["v"].Type.Is("reflect.Value")).
		Report("use range $v.Methods() instead of range $v.NumMethod() (Go 1.26+)")
}

// ReflectInsOutsIterator detects manual index-based iteration over function
// input and output parameters and suggests using the iterators added in Go 1.26.
//
// Old patterns:
//
//	for i := 0; i < t.NumIn(); i++ {
//	    param := t.In(i)
//	    // use param
//	}
//	for i := 0; i < t.NumOut(); i++ {
//	    ret := t.Out(i)
//	    // use ret
//	}
//
// New patterns (Go 1.26+):
//
//	for param := range t.Ins() {
//	    // use param
//	}
//	for ret := range t.Outs() {
//	    // use ret
//	}
//
// Benefits:
//   - Cleaner, more idiomatic Go iteration
//   - Consistent with Fields() and Methods() iterators
//
// See: https://pkg.go.dev/reflect#Type.Ins
// See: https://pkg.go.dev/reflect#Type.Outs
func ReflectInsOutsIterator(m dsl.Matcher) {
	// NumIn loop
	m.Match(
		`for $i := 0; $i < $t.NumIn(); $i++ { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Ins() instead of index-based input parameter iteration (Go 1.26+)")

	// NumOut loop
	m.Match(
		`for $i := 0; $i < $t.NumOut(); $i++ { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Outs() instead of index-based output parameter iteration (Go 1.26+)")

	// range over NumIn/NumOut integer
	m.Match(
		`for $i := range $t.NumIn() { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Ins() instead of range $t.NumIn() (Go 1.26+)")

	m.Match(
		`for $i := range $t.NumOut() { $*_ }`,
	).
		Where(m["t"].Type.Is("reflect.Type")).
		Report("use range $t.Outs() instead of range $t.NumOut() (Go 1.26+)")
}
