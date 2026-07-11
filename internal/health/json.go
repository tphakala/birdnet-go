// internal/health/json.go
package health

import (
	"encoding/json"
	"math"
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
// (NaN, +Inf, -Inf) replaced by nil. Health-check Details hold primitives,
// float slices, and nested maps/slices; those shapes are walked recursively and
// any other value is returned unchanged.
func sanitizeValue(v any) any {
	switch x := v.(type) {
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
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sanitizeValue(e)
		}
		return out
	case []float64:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sanitizeValue(e)
		}
		return out
	case []float32:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sanitizeValue(e)
		}
		return out
	case map[string]any:
		return sanitizeMap(x)
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
