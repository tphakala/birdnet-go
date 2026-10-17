package onnx

import (
	"os"
	"strings"
)

const (
	// ortDisableTelemetryEnv is the environment variable ONNX Runtime reads to
	// switch off its built-in Microsoft telemetry.
	ortDisableTelemetryEnv = "ORT_DISABLE_TELEMETRY"
	// ortDisableTelemetryValue is the value BirdNET-Go writes to switch it off.
	ortDisableTelemetryValue = "1"
)

// ortTelemetryTruthyTokens are the values ONNX Runtime treats as "disable
// telemetry" (matched case-insensitively after trimming), per
// onnxruntime/core/platform/telemetry_environment.h at v1.31.0, lines 279-285
// as recorded in the upgrade plan (not re-read when this file was written).
var ortTelemetryTruthyTokens = []string{"1", "true", "yes", "on", "y"}

// TelemetryOptOut reports the state of the ONNX Runtime telemetry opt-out.
type TelemetryOptOut struct {
	// Value is the effective ORT_DISABLE_TELEMETRY value.
	Value string
	// Disabled is true when Value switches telemetry off by ONNX Runtime's rule.
	Disabled bool
	// APIDisabled is true when ort.DisableTelemetry succeeded after loading.
	APIDisabled bool
}

// ortTelemetryValueDisables reports whether v is a value ONNX Runtime reads as
// "disable telemetry".
func ortTelemetryValueDisables(v string) bool {
	v = strings.TrimSpace(v)
	for _, token := range ortTelemetryTruthyTokens {
		if strings.EqualFold(v, token) {
			return true
		}
	}
	return false
}

// applyTelemetryOptOut sets ORT_DISABLE_TELEMETRY=1 through set, unless lookup
// reports a non-blank value, which is a user choice and is never overwritten.
// It is idempotent.
func applyTelemetryOptOut(lookup func(string) (string, bool), set func(string, string) error) (TelemetryOptOut, error) {
	if v, ok := lookup(ortDisableTelemetryEnv); ok && strings.TrimSpace(v) != "" {
		return TelemetryOptOut{Value: v, Disabled: ortTelemetryValueDisables(v)}, nil
	}
	if err := set(ortDisableTelemetryEnv, ortDisableTelemetryValue); err != nil {
		return TelemetryOptOut{}, err
	}
	return TelemetryOptOut{Value: ortDisableTelemetryValue, Disabled: true}, nil
}

// ensureTelemetryOptOut applies the opt-out to the process environment.
func ensureTelemetryOptOut() (TelemetryOptOut, error) {
	return applyTelemetryOptOut(os.LookupEnv, os.Setenv)
}

// init switches ONNX Runtime telemetry off in every binary that can load it.
// It is a deliberate side effect (privacy), despite the guideline against
// init() side effects: it runs before main and before any cgo thread could call
// getenv, which matters because a C setenv racing a getenv is unsafe. The
// error is dropped here; InitORT repeats the same idempotent call and returns
// any error.
func init() {
	_, _ = ensureTelemetryOptOut()
}
