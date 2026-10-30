package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// assertNativeGate runs the shared case matrix every "native" opt-in gate obeys:
// only the value "native" (case-insensitive, whitespace trimmed) enables it;
// unset or anything else keeps the FFmpeg path.
func assertNativeGate(t *testing.T, env string, enabled func() bool) {
	t.Helper()
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset keeps ffmpeg", value: "", want: false},
		{name: "native opts in", value: "native", want: true},
		{name: "uppercase opts in", value: "NATIVE", want: true},
		{name: "mixed case opts in", value: "Native", want: true},
		{name: "surrounding whitespace opts in", value: "  native ", want: true},
		{name: "ffmpeg stays ffmpeg", value: "ffmpeg", want: false},
		{name: "truthy value is not enough", value: "1", want: false},
		{name: "typo stays ffmpeg", value: "nativ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(env, tt.value)
			assert.Equal(t, tt.want, enabled())
		})
	}
}

func TestNativeStreamIngestEnabled(t *testing.T) {
	assertNativeGate(t, EnvNativeStreamIngest, NativeStreamIngestEnabled)
}
