package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLowMemoryConfigGetMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode string
		want string
	}{
		{"empty defaults to auto", "", LowMemoryModeAuto},
		{"auto", "auto", LowMemoryModeAuto},
		{"on", "on", LowMemoryModeOn},
		{"off", "off", LowMemoryModeOff},
		{"unknown defaults to auto", "lowmem", LowMemoryModeAuto},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := LowMemoryConfig{Mode: tt.mode}
			assert.Equal(t, tt.want, c.GetMode())
		})
	}
}

func TestValidateNormalizesInvalidLowMemoryMode(t *testing.T) {
	s := createMinimalValidSettings()
	s.LowMemory.Mode = "bogus"

	require.NoError(t, ValidateSettings(s))

	assert.Equal(t, LowMemoryModeAuto, s.LowMemory.Mode, "invalid mode should normalize to auto")
	assert.Contains(t, strings.Join(s.ValidationWarnings, " "), "lowmemory.mode",
		"a validation warning should record the invalid mode")
}

func TestValidatePreservesValidLowMemoryMode(t *testing.T) {
	s := createMinimalValidSettings()
	s.LowMemory.Mode = LowMemoryModeOn

	require.NoError(t, ValidateSettings(s))

	assert.Equal(t, LowMemoryModeOn, s.LowMemory.Mode, "valid mode must be preserved")
}

func TestValidateCanonicalizesLowMemoryModeCase(t *testing.T) {
	s := createMinimalValidSettings()
	s.LowMemory.Mode = " On "

	require.NoError(t, ValidateSettings(s))

	assert.Equal(t, LowMemoryModeOn, s.LowMemory.Mode,
		"case and whitespace should canonicalize, preserving the operator's intent")
	assert.NotContains(t, strings.Join(s.ValidationWarnings, " "), "lowmemory.mode",
		"a valid (if cased) mode should not produce a warning")
}
