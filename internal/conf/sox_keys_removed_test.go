package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestLoad_LeftoverSoxKeysLoadAndAreDroppedOnSave verifies that a config written
// before the SoX settings were removed still loads through the real loader, and
// that the next save no longer writes the keys. Not parallel: the loader keeps
// global state.
func TestLoad_LeftoverSoxKeysLoadAndAreDroppedOnSave(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
realtime:
  audio:
    soxpath: /usr/bin/sox
    soxaudiotypes: [wav, mp3]
    export:
      enabled: true
      type: wav
      path: clips/
      length: 15
`
	settings, _ := loadConfigFromYAML(t, cfg)

	savePath := filepath.Join(t.TempDir(), "saved.yaml")
	require.NoError(t, SaveYAMLConfig(savePath, settings))

	data, err := os.ReadFile(savePath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	var saved map[string]any
	require.NoError(t, yaml.Unmarshal(data, &saved))

	realtime, ok := saved["realtime"].(map[string]any)
	require.True(t, ok, "saved config must have a realtime section")
	audio, ok := realtime["audio"].(map[string]any)
	require.True(t, ok, "saved config must have a realtime.audio section")
	assert.NotContains(t, audio, "soxpath", "the removed SoX path key must not be written back")
	assert.NotContains(t, audio, "soxaudiotypes", "the removed SoX audio types key must not be written back")
}
