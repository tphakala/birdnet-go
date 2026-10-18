package support

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// TestSupportDumpRedactsSecurityAddressLists marshals a real conf.Settings so
// the yaml tags of Security.TrustedProxies and Security.PlainHTTPSessionClients
// are part of what is asserted.
func TestSupportDumpRedactsSecurityAddressLists(t *testing.T) {
	t.Parallel()

	addresses := []string{"198.51.100.20", "10.0.0.0/8", "172.18.0.5"}

	settings := &conf.Settings{}
	settings.Security.TrustedProxies = []string{"cloudflare", "198.51.100.20", "10.0.0.0/8"}
	settings.Security.PlainHTTPSessionClients = []string{"172.18.0.5"}

	data, err := yaml.Marshal(settings)
	require.NoError(t, err)
	for _, addr := range addresses {
		require.Contains(t, string(data), addr, "test setup must write the address into the config file")
	}

	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0o600))

	collector := NewCollector(configDir, t.TempDir(), "test-system", "test-version")
	scrubbed, err := collector.collectConfig(true)
	require.NoError(t, err)

	rendered, err := yaml.Marshal(scrubbed)
	require.NoError(t, err)
	for _, addr := range addresses {
		assert.NotContains(t, string(rendered), addr)
	}

	security, ok := scrubbed["security"].(map[string]any)
	require.True(t, ok, "security section should survive scrubbing: %#v", scrubbed)
	assert.Equal(t,
		[]any{"cloudflare", "[redacted public address]", "[redacted private range]"},
		security["trustedproxies"])
	assert.Equal(t, []any{"[redacted private address]"}, security["plainhttpsessionclients"])
}
