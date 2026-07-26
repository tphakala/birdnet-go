// internal/health/json_test.go
package health

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDiagnosticsReportMarshalJSON_NonFinite verifies that a report containing
// non-finite floats (which encoding/json rejects) still marshals to valid JSON,
// with the offending values replaced by null (or 0 for duration fields) and
// finite values left intact. Nested typed containers (e.g. []map[string]any and
// map[string]float64) must be sanitized too, since Go's type system would let
// them bypass a plain type switch.
func TestDiagnosticsReportMarshalJSON_NonFinite(t *testing.T) {
	t.Parallel()

	r := DiagnosticsReport{
		ID:         "test",
		DurationMS: math.Inf(1),
		Results: []Result{{
			Name:       "cpu",
			Category:   CategorySystem,
			Status:     StatusWarning,
			DurationMS: math.NaN(),
			Details: map[string]any{
				"percent":          math.Inf(1),
				"per_core_percent": []float64{12.5, math.Inf(-1), 30.0},
				"nested":           map[string]any{"rate": math.NaN(), "ok": 3},
				"slice_of_maps":    []map[string]any{{"val": math.NaN()}},
				"typed_map":        map[string]float64{"val": math.Inf(1)},
				"finite":           42.0,
				"label":            "cpu0",
			},
			Timestamp: time.Unix(0, 0).UTC(),
		}},
	}

	b, err := json.Marshal(r)
	require.NoError(t, err)

	// No non-finite tokens leak into the output.
	for _, bad := range []string{"Inf", "inf", "NaN"} {
		assert.NotContains(t, string(b), bad)
	}

	// Output round-trips as valid JSON.
	var back map[string]any
	require.NoError(t, json.Unmarshal(b, &back))

	assert.InDelta(t, 0.0, back["duration_ms"], 0)

	results, ok := back["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	res0, ok := results[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 0.0, res0["duration_ms"], 0)

	details, ok := res0["details"].(map[string]any)
	require.True(t, ok)

	assert.Nil(t, details["percent"])
	assert.InDelta(t, 42.0, details["finite"], 0)
	assert.Equal(t, "cpu0", details["label"])

	perCore, ok := details["per_core_percent"].([]any)
	require.True(t, ok)
	require.Len(t, perCore, 3)
	assert.InDelta(t, 12.5, perCore[0], 0)
	assert.Nil(t, perCore[1])
	assert.InDelta(t, 30.0, perCore[2], 0)

	nested, ok := details["nested"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, nested["rate"])
	assert.InDelta(t, 3.0, nested["ok"], 0)

	sliceOfMaps, ok := details["slice_of_maps"].([]any)
	require.True(t, ok)
	require.Len(t, sliceOfMaps, 1)
	firstMap, ok := sliceOfMaps[0].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, firstMap["val"])

	typedMap, ok := details["typed_map"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, typedMap["val"])
}

// sampleErrorGroup mirrors the shape checks store under details["top_errors"]:
// a struct slice whose SampleFields carries arbitrary log-field values. A
// non-finite float nested inside a struct field must not defeat serialization,
// and a json.Marshaler field (time.Time) must survive unchanged.
type sampleErrorGroup struct {
	Component    string         `json:"component,omitempty"`
	Message      string         `json:"message"`
	Count        int            `json:"count"`
	When         time.Time      `json:"when"`
	SampleFields map[string]any `json:"sample_fields,omitempty"`
	Internal     string         `json:"-"`
	unexported   int
}

// TestResultMarshalJSON_NonFiniteInStruct verifies that a non-finite float
// nested inside a struct value in Details (e.g. top_errors[].sample_fields)
// does not break encoding, that the struct's other fields (including a
// time.Time) are preserved, and that omitempty and json:"-" tags are honored.
func TestResultMarshalJSON_NonFiniteInStruct(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 7, 12, 8, 30, 0, 0, time.UTC)
	group := sampleErrorGroup{
		Message: "boom",
		Count:   2,
		When:    when,
		SampleFields: map[string]any{
			"rate": math.Inf(1),
			"ok":   7,
		},
		Internal:   "should-be-dropped",
		unexported: 99,
	}
	// Reference the unexported field so it is not reported as unused; the
	// sanitizer must still skip it (reflect would panic on Interface() otherwise).
	require.Equal(t, 99, group.unexported)

	r := Result{
		Name:     "recent_errors",
		Category: CategoryLogs,
		Status:   StatusWarning,
		Details: map[string]any{
			"top_errors": []sampleErrorGroup{group},
		},
		Timestamp: time.Unix(0, 0).UTC(),
	}

	b, err := json.Marshal(r)
	require.NoError(t, err)
	for _, bad := range []string{"Inf", "inf", "NaN", "should-be-dropped"} {
		assert.NotContains(t, string(b), bad)
	}

	var back map[string]any
	require.NoError(t, json.Unmarshal(b, &back))
	details, ok := back["details"].(map[string]any)
	require.True(t, ok)
	topErrors, ok := details["top_errors"].([]any)
	require.True(t, ok)
	require.Len(t, topErrors, 1)
	got, ok := topErrors[0].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "boom", got["message"])
	assert.InDelta(t, 2.0, got["count"], 0)
	// time.Time survives as its RFC3339 string, not walked into a struct.
	assert.Equal(t, when.Format(time.RFC3339Nano), got["when"])
	// omitempty empty field and json:"-" field are absent.
	assert.NotContains(t, got, "component")
	assert.NotContains(t, got, "Internal")

	fields, ok := got["sample_fields"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, fields["rate"])
	assert.InDelta(t, 7.0, fields["ok"], 0)
}

// TestSanitizeValue_IntKeyedMapPreserved verifies that a non-string-keyed map is
// re-keyed (not silently dropped) when it has to be sanitized.
func TestSanitizeValue_IntKeyedMapPreserved(t *testing.T) {
	t.Parallel()

	in := map[int]any{1: math.Inf(1), 2: 5.0}
	out, ok := sanitizeValue(in).(map[string]any)
	require.True(t, ok)
	require.Len(t, out, 2)
	assert.Nil(t, out["1"])
	assert.InDelta(t, 5.0, out["2"], 0)
}

// TestResultMarshalJSON_FiniteFastPath verifies that an all-finite result is
// emitted byte-for-byte identically to the plain (unsanitized) struct, so the
// sanitizer is transparent on the common path.
func TestResultMarshalJSON_FiniteFastPath(t *testing.T) {
	t.Parallel()

	r := Result{
		Name:     "cpu",
		Category: CategorySystem,
		Status:   StatusHealthy,
		Details: map[string]any{
			"percent":          12.5,
			"per_core_percent": []float64{10.0, 20.0},
			"count":            3,
		},
		DurationMS: 1.25,
		Timestamp:  time.Unix(0, 0).UTC(),
	}

	got, err := json.Marshal(r)
	require.NoError(t, err)

	type alias Result
	want, err := json.Marshal(alias(r))
	require.NoError(t, err)

	assert.JSONEq(t, string(want), string(got))
}

// TestSanitizeValue_FiniteUnchanged confirms sanitizeValue leaves finite and
// non-float values untouched.
func TestSanitizeValue_FiniteUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{"finite_float", 3.14, 3.14},
		{"string", "x", "x"},
		{"int", 7, 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sanitizeValue(tc.input), "value should be unchanged")
		})
	}
}
