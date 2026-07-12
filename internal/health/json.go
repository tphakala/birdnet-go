// internal/health/json.go
package health

import (
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// MarshalJSON implements json.Marshaler for DiagnosticsReport. It zeroes the
// always-present duration field when it is non-finite (NaN, +Inf, -Inf) and
// relies on Result.MarshalJSON to sanitize each check's nested values.
//
// encoding/json returns an "unsupported value" error for non-finite floats, so
// without this a single check emitting a non-finite metric (e.g. a rate or
// percentage computed against a zero denominator) makes the whole response
// fail to encode. The System Health page then shows "Failed to load
// diagnostics" instead of the report. Sanitizing at the serialization boundary
// keeps one bad metric from taking down the entire diagnostics payload,
// regardless of which check produced it.
func (r DiagnosticsReport) MarshalJSON() ([]byte, error) { //nolint:gocritic // hugeParam: json.Marshaler requires a value receiver so json.Marshal invokes it when a value (not pointer) is passed
	type alias DiagnosticsReport // sheds MarshalJSON to avoid infinite recursion
	clone := alias(r)
	clone.DurationMS = finiteOrZero(clone.DurationMS)
	// Results are []Result and are sanitized by Result.MarshalJSON during encoding.
	return json.Marshal(clone)
}

// MarshalJSON implements json.Marshaler for Result. It zeroes a non-finite
// duration and, only when the check actually emitted a non-finite float
// somewhere in Details, sanitizes Details so the payload still encodes. See
// DiagnosticsReport.MarshalJSON.
func (r Result) MarshalJSON() ([]byte, error) { //nolint:gocritic // hugeParam: json.Marshaler requires a value receiver so json.Marshal invokes it when a value (not pointer) is passed
	type alias Result // sheds MarshalJSON to avoid infinite recursion
	clone := alias(r)
	clone.DurationMS = finiteOrZero(clone.DurationMS)

	// Fast path: the overwhelmingly common case is an all-finite report, which
	// marshals cleanly and byte-for-byte identically to the unsanitized struct.
	if b, err := json.Marshal(clone); err == nil {
		return b, nil
	}

	// Slow path: a non-finite float is nested somewhere in Details. Replace the
	// offending values with null and retry. sanitizeValue only descends into
	// subtrees that fail to marshal, so finite data is preserved verbatim.
	if clone.Details != nil {
		clone.Details = sanitizeMap(clone.Details)
	}
	return json.Marshal(clone)
}

// finiteOrZero returns f, or 0 when f is NaN or infinite. Used for numeric
// fields that are always present in the JSON schema (durations), where a null
// would change the field's type for consumers.
func finiteOrZero(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// sanitizeMap applies sanitizeValue to every value in m, returning a new map so
// the caller's Details are not mutated.
func sanitizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = sanitizeValue(v)
	}
	return out
}

// sanitizeValue returns a JSON-safe copy of v in which every non-finite float
// (NaN, +Inf, -Inf) has been replaced by nil. Any value that already marshals
// cleanly is returned unchanged, so sanitizeValue only descends into the
// subtrees that actually contain a non-finite float. Slices, arrays, maps, and
// structs are walked generically (structs field-by-field, honoring json tags)
// because Go's type system would let a non-finite float nested in a typed
// container or a struct field slip past a plain type switch. Anything that
// still cannot be represented is replaced by nil so the surrounding payload
// encodes.
func sanitizeValue(v any) any {
	if v == nil {
		return nil
	}
	if _, err := json.Marshal(v); err == nil {
		return v
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		// A lone float only fails to marshal when it is non-finite.
		return nil
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = sanitizeValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[mapKeyString(iter.Key())] = sanitizeValue(iter.Value().Interface())
		}
		return out
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return sanitizeValue(rv.Elem().Interface())
	case reflect.Struct:
		return sanitizeStruct(rv)
	default:
		// Channels, funcs, complex numbers, etc. are not JSON-representable;
		// drop them rather than fail the whole payload.
		return nil
	}
}

// mapKeyString renders a map key the way encoding/json would, so sanitized maps
// keep their keys instead of silently dropping non-string-keyed entries.
func mapKeyString(k reflect.Value) string {
	if k.Kind() == reflect.String {
		return k.String()
	}
	if tm, ok := reflect.TypeAssert[encoding.TextMarshaler](k); ok {
		if b, err := tm.MarshalText(); err == nil {
			return string(b)
		}
	}
	switch k.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(k.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(k.Uint(), 10)
	default:
		return fmt.Sprint(k.Interface())
	}
}

// sanitizeStruct walks a struct value field-by-field, producing a map keyed by
// each exported field's JSON name with sanitized values. It mirrors
// encoding/json's tag handling (name override, "-" to skip, omitempty, and
// promotion of anonymous struct fields) closely enough that the sanitized
// object keeps the shape consumers expect. It is only reached when a struct
// actually contains a non-finite float, since clean structs are returned
// verbatim by sanitizeValue.
func sanitizeStruct(rv reflect.Value) map[string]any {
	out := make(map[string]any)
	addStructFields(out, rv)
	return out
}

// addStructFields adds rv's exported fields to out, recursing into anonymous
// struct fields so their fields are promoted like encoding/json does.
func addStructFields(out map[string]any, rv reflect.Value) {
	for f, fv := range rv.Fields() {
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}

		// Promote anonymous struct fields that have no explicit JSON name.
		if f.Anonymous && !hasExplicitJSONName(tag) {
			ev := fv
			if ev.Kind() == reflect.Pointer {
				if ev.IsNil() {
					continue
				}
				ev = ev.Elem()
			}
			if ev.Kind() == reflect.Struct {
				addStructFields(out, ev)
				continue
			}
		}

		name, omitempty := parseJSONFieldTag(tag, f.Name)
		if omitempty && isEmptyValue(fv) {
			continue
		}
		out[name] = sanitizeValue(fv.Interface())
	}
}

// hasExplicitJSONName reports whether a json struct tag sets an explicit field
// name (e.g. `json:"foo"` or `json:"foo,omitempty"`, but not `json:",omitempty"`).
func hasExplicitJSONName(tag string) bool {
	if tag == "" {
		return false
	}
	name, _, _ := strings.Cut(tag, ",")
	return name != ""
}

// parseJSONFieldTag returns the JSON name and omitempty flag for a struct field,
// defaulting to fieldName when the tag does not override the name.
func parseJSONFieldTag(tag, fieldName string) (name string, omitempty bool) {
	name = fieldName
	if tag == "" {
		return name, false
	}
	first, rest, _ := strings.Cut(tag, ",")
	if first != "" {
		name = first
	}
	for rest != "" {
		var opt string
		opt, rest, _ = strings.Cut(rest, ",")
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

// isEmptyValue reports whether v is empty using the same definition
// encoding/json applies for the omitempty option.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	default:
		return false
	}
}
