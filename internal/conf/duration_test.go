package conf

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDuration_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		duration Duration
		expected string
	}{
		{"zero", Duration(0), `"0s"`},
		{"30 seconds", Duration(30 * time.Second), `"30s"`},
		{"5 minutes", Duration(5 * time.Minute), `"5m0s"`},
		{"1 hour", Duration(time.Hour), `"1h0m0s"`},
		{"10 seconds", Duration(10 * time.Second), `"10s"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := json.Marshal(tt.duration)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, string(b))
		})
	}
}

func TestDuration_UnmarshalJSON_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected Duration
	}{
		{"30s string", `"30s"`, Duration(30 * time.Second)},
		{"5m string", `"5m"`, Duration(5 * time.Minute)},
		{"1h string", `"1h"`, Duration(time.Hour)},
		{"0s string", `"0s"`, Duration(0)},
		{"complex", `"1h30m10s"`, Duration(time.Hour + 30*time.Minute + 10*time.Second)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d Duration
			err := json.Unmarshal([]byte(tt.input), &d)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, d)
		})
	}
}

func TestDuration_UnmarshalJSON_Number(t *testing.T) {
	t.Parallel()

	// Backward compat: numbers are nanoseconds
	var d Duration
	err := json.Unmarshal([]byte(`30000000000`), &d)
	require.NoError(t, err)
	assert.Equal(t, Duration(30*time.Second), d)
}

func TestDuration_UnmarshalJSON_Null(t *testing.T) {
	t.Parallel()

	// null should reset to zero (matches standard json.Unmarshal behavior)
	d := Duration(30 * time.Second)
	err := json.Unmarshal([]byte(`null`), &d)
	require.NoError(t, err)
	assert.Equal(t, Duration(0), d)
}

func TestDuration_UnmarshalJSON_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"invalid string", `"notaduration"`},
		{"boolean", `true`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var d Duration
			err := json.Unmarshal([]byte(tt.input), &d)
			assert.Error(t, err)
		})
	}
}

func TestDuration_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	// Simulate the settings PATCH round-trip that was corrupting durations
	type Config struct {
		Timeout Duration `json:"timeout"`
	}

	original := Config{Timeout: Duration(30 * time.Second)}

	// Marshal to JSON (like mergeJSONIntoStruct does)
	b, err := json.Marshal(original)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"30s"`)

	// Unmarshal through map (like deepMergeMaps does)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))

	// Re-marshal and unmarshal back to struct
	b2, err := json.Marshal(m)
	require.NoError(t, err)

	var result Config
	require.NoError(t, json.Unmarshal(b2, &result))
	assert.Equal(t, original.Timeout, result.Timeout, "duration should survive JSON round-trip through map")
}

func TestDuration_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	type Config struct {
		Timeout Duration `yaml:"timeout"`
	}

	original := Config{Timeout: Duration(30 * time.Second)}

	b, err := yaml.Marshal(original)
	require.NoError(t, err)
	assert.Contains(t, string(b), "30s")

	var result Config
	require.NoError(t, yaml.Unmarshal(b, &result))
	assert.Equal(t, original.Timeout, result.Timeout, "duration should survive YAML round-trip")
}

func TestDuration_YAMLBackwardCompat_NumericNanoseconds(t *testing.T) {
	t.Parallel()

	// Legacy configs may have bare integer nanosecond values from the corruption bug
	type Config struct {
		Timeout Duration `yaml:"timeout"`
	}

	// Simulate a legacy config with "timeout: 30000000000" (30s in nanoseconds)
	var result Config
	err := yaml.Unmarshal([]byte("timeout: 30000000000"), &result)
	require.NoError(t, err)
	assert.Equal(t, Duration(30*time.Second), result.Timeout, "bare integer YAML value should be treated as nanoseconds")

	// Also handle small corrupted values like "timeout: 300" (300ns)
	var result2 Config
	err = yaml.Unmarshal([]byte("timeout: 300"), &result2)
	require.NoError(t, err)
	assert.Equal(t, Duration(300), result2.Timeout, "small bare integer should be treated as nanoseconds")
}

func TestDuration_Std(t *testing.T) {
	t.Parallel()

	d := Duration(30 * time.Second)
	assert.Equal(t, 30*time.Second, d.Std())
}
