// internal/health/json.go
package health

import (
	"encoding/json"
	"math"
	"reflect"
)

// MarshalJSON implements json.Marshaler for DiagnosticsReport. It replaces any
// non-finite float (NaN, +Inf, -Inf) produced by a health check with JSON null
// (or 0 for the always-present duration fields) before encoding.
//
// encoding/json returns an "unsupported value" error for non-finite floats, so
// without this a single check emitting a non-finite metric — e.g. a rate or
// percentage computed against a zero denominator — makes the whole response
// fail to encode. The System Health page then shows "Failed to load
// diagnostics" instead of the report. Sanitizing at the serialization boundary
// keeps one bad metric from taking down the entire diagnostics payload,
// regardless of which check produced it.
func (r DiagnosticsReport) MarshalJSON() ([]byte, error) {
	type alias DiagnosticsReport // sheds MarshalJSON to avoid infinite recursion
	clone := alias(r)
	clone.DurationMS = finiteOrZero(clone.DurationMS)
	// Results are []Result and are sanitized by Result.MarshalJSON during encoding.
	return json.Marshal(clone)
}

// MarshalJSON implements json.Marshaler for Result, sanitizing its duration and
// any non-finite floats nested in Details. See DiagnosticsReport.MarshalJSON.
func (r Result) MarshalJSON() ([]byte, error) {
	type alias Result // sheds MarshalJSON to avoid infinite recursion
	clone := alias(r)
	clone.DurationMS = finiteOrZero(clone.DurationMS)
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

// sanitizeValue returns a JSON-safe copy of v with every non-finite float
// (NaN, +Inf, -Inf) replaced by nil. Scalars take a fast path; slices, arrays,
// and maps are walked generically via reflection. Reflection is required
// because Go slices/maps are not covariant, so a type switch would miss the
// nested shapes health checks can produce (e.g. []map[string]any or
// map[string]float64), letting a non-finite float slip through and break
// encoding/json. Any other value (structs, etc.) is returned unchanged.
func sanitizeValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case float32:
		if f := float64(x); math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return x
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return v
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
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
			// JSON object keys are strings; skip any non-string-keyed map.
			if k := iter.Key(); k.Kind() == reflect.String {
				out[k.String()] = sanitizeValue(iter.Value().Interface())
			}
		}
		return out
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return sanitizeValue(rv.Elem().Interface())
	default:
		return v
	}
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
