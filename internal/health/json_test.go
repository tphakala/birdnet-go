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

	assert.Equal(t, 0.0, back["duration_ms"])

	results, ok := back["results"].([]any)
	require.True(t, ok)
	require.Len(t, results, 1)
	res0, ok := results[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, 0.0, res0["duration_ms"])

	details, ok := res0["details"].(map[string]any)
	require.True(t, ok)

	assert.Nil(t, details["percent"])
	assert.Equal(t, 42.0, details["finite"])
	assert.Equal(t, "cpu0", details["label"])

	perCore, ok := details["per_core_percent"].([]any)
	require.True(t, ok)
	require.Len(t, perCore, 3)
	assert.Equal(t, 12.5, perCore[0])
	assert.Nil(t, perCore[1])
	assert.Equal(t, 30.0, perCore[2])

	nested, ok := details["nested"].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, nested["rate"])
	assert.Equal(t, 3.0, nested["ok"])

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
