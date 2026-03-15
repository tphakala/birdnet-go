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
