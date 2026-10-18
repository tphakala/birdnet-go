package support

import (
	"archive/zip"
	"bytes"
	"io"
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

// TestSupportArchiveRedactsSecurityAddressLists covers the config.yaml written
// into the archive, which has its own read and scrub path.
func TestSupportArchiveRedactsSecurityAddressLists(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Security.TrustedProxies = []string{"cloudflare", "198.51.100.20"}
	settings.Security.PlainHTTPSessionClients = []string{"172.18.0.5"}

	data, err := yaml.Marshal(settings)
	require.NoError(t, err)

	configDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), data, 0o600))

	collector := NewCollector(configDir, t.TempDir(), "test-system", "test-version")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	require.NoError(t, collector.addConfigToArchive(zw, true))
	require.NoError(t, zw.Close())

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)
	require.Len(t, zr.File, 1)
	rc, err := zr.File[0].Open()
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	archived, err := io.ReadAll(rc)
	require.NoError(t, err)

	assert.NotContains(t, string(archived), "198.51.100.20")
	assert.NotContains(t, string(archived), "172.18.0.5")
	assert.Contains(t, string(archived), "[redacted public address]")
	assert.Contains(t, string(archived), "[redacted private address]")
	assert.Contains(t, string(archived), "cloudflare")
}
