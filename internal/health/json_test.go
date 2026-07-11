// internal/health/json_test.go
package health

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// TestDiagnosticsReportMarshalJSON_NonFinite verifies that a report containing
// non-finite floats (which encoding/json rejects) still marshals to valid JSON,
// with the offending values replaced by null (or 0 for duration fields) and
// finite values left intact.
func TestDiagnosticsReportMarshalJSON_NonFinite(t *testing.T) {
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
				"finite":           42.0,
				"label":            "cpu0",
			},
			Timestamp: time.Unix(0, 0).UTC(),
		}},
	}

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}

	// No non-finite tokens leak into the output.
	for _, bad := range []string{"Inf", "inf", "NaN"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("output contains %q: %s", bad, b)
		}
	}

	// Output round-trips as valid JSON.
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if back["duration_ms"] != 0.0 {
		t.Errorf("report duration_ms = %v, want 0", back["duration_ms"])
	}

	res0 := back["results"].([]any)[0].(map[string]any)
	if res0["duration_ms"] != 0.0 {
		t.Errorf("result duration_ms = %v, want 0", res0["duration_ms"])
	}

	details := res0["details"].(map[string]any)
	if details["percent"] != nil {
		t.Errorf("percent = %v, want null", details["percent"])
	}
	if details["finite"] != 42.0 {
		t.Errorf("finite = %v, want 42", details["finite"])
	}
	if details["label"] != "cpu0" {
		t.Errorf("label = %v, want cpu0", details["label"])
	}
	if got := details["per_core_percent"].([]any); got[0] != 12.5 || got[1] != nil || got[2] != 30.0 {
		t.Errorf("per_core_percent = %v, want [12.5, null, 30]", got)
	}
	if nested := details["nested"].(map[string]any); nested["rate"] != nil || nested["ok"] != 3.0 {
		t.Errorf("nested = %v, want {rate:null, ok:3}", nested)
	}
}

// TestSanitizeValue_FiniteUnchanged confirms sanitizeValue leaves finite and
// non-float values untouched.
func TestSanitizeValue_FiniteUnchanged(t *testing.T) {
	if got := sanitizeValue(3.14); got != 3.14 {
		t.Errorf("finite float changed: %v", got)
	}
	if got := sanitizeValue("x"); got != "x" {
		t.Errorf("string changed: %v", got)
	}
	if got := sanitizeValue(7); got != 7 {
		t.Errorf("int changed: %v", got)
	}
}
