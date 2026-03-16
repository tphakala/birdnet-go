package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigToGothProvider_ContainsOIDC(t *testing.T) {
	t.Parallel()

	gothName, ok := ConfigToGothProvider[ConfigOIDC]
	assert.True(t, ok, "ConfigToGothProvider should contain OIDC mapping")
	assert.Equal(t, ProviderOIDC, gothName)
}

func TestGetGothProviderName_OIDC(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ProviderOIDC, GetGothProviderName(ConfigOIDC))
}

func TestGetGothProviderName_UnknownFallback(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "unknown", GetGothProviderName("unknown"))
}

// TestGothToConfigProvider_ReverseMapping verifies the reverse map contains all entries.
func TestGothToConfigProvider_ReverseMapping(t *testing.T) {
	t.Parallel()

	// Verify every entry in ConfigToGothProvider has a reverse mapping
	for config, goth := range ConfigToGothProvider {
		reverseConfig, ok := GothToConfigProvider[goth]
		assert.True(t, ok, "GothToConfigProvider should contain mapping for goth name %q", goth)
		assert.Equal(t, config, reverseConfig, "reverse mapping for %q should be %q", goth, config)
	}

	// Verify the maps have the same size
	assert.Len(t, GothToConfigProvider, len(ConfigToGothProvider),
		"GothToConfigProvider should have same number of entries as ConfigToGothProvider")
}

// TestGothToConfigProvider_SpecificMappings verifies the key reverse mappings.
func TestGothToConfigProvider_SpecificMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		gothProvider   string
		configProvider string
	}{
		{name: "Microsoft reverse", gothProvider: ProviderMicrosoft, configProvider: ConfigMicrosoft},
		{name: "OIDC reverse", gothProvider: ProviderOIDC, configProvider: ConfigOIDC},
		{name: "Google reverse", gothProvider: ProviderGoogle, configProvider: ConfigGoogle},
		{name: "GitHub reverse", gothProvider: ProviderGitHub, configProvider: ConfigGitHub},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.configProvider, gothToConfigProvider(tt.gothProvider))
		})
	}
}

// TestGothToConfigProvider_UnknownFallback verifies the fallback for unknown providers.
func TestGothToConfigProvider_UnknownFallback(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "unknown-provider", gothToConfigProvider("unknown-provider"))
}

// TestSessionKeyAuthProviderConstant verifies the constant value.
func TestSessionKeyAuthProviderConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "auth_provider", SessionKeyAuthProvider)
}
