package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// loadConfigFromYAML writes cfg to a temp config.yaml, points the loader at it,
// and returns the loaded settings. It restores the global loader state (ConfigPath,
// viper, published settings) on cleanup so sequential tests do not leak into each
// other. Not safe for t.Parallel().
func loadConfigFromYAML(t *testing.T, cfg string) (settings *Settings, configPath string) {
	t.Helper()
	configPath = filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(cfg), 0o600))

	oldPath := ConfigPath
	oldSettings := CloneSettings(GetSettings())
	t.Cleanup(func() {
		ConfigPath = oldPath
		viper.Reset()
		StoreSettings(oldSettings)
	})

	viper.Reset()
	ConfigPath = configPath

	settings, err := Load()
	require.NoError(t, err, "a config with a lossy export and no bitrate must load")
	return settings, configPath
}

// readPersistedSettings unmarshals the on-disk config.yaml directly (bypassing the
// load-time normalization), so a test can assert what was actually written to disk
// rather than what the runtime holds in memory.
func readPersistedSettings(t *testing.T, configPath string) *Settings {
	t.Helper()
	data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	var s Settings
	require.NoError(t, yaml.Unmarshal(data, &s))
	return &s
}

// A config that carries an explicit empty bitrate (bitrate: "") for a lossy export
// unmarshals with an empty bitrate. That is the on-disk shape a disabled-time save
// or a hand-edit leaves behind. The escalating Sentry warning came from repairing
// that in memory on every load without ever writing the repair back. The load path
// must now heal the file once: the in-memory value is the documented default, the
// file on disk carries it, and a second load does not warn again.
func TestLoad_EmptyLossyExportBitrate_SelfHealsAndStopsWarning(t *testing.T) {
	for _, exportType := range []string{AudioExportTypeAAC, AudioExportTypeOPUS, AudioExportTypeMP3} {
		t.Run(exportType, func(t *testing.T) {
			cfg := `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
realtime:
  audio:
    export:
      enabled: true
      type: ` + exportType + `
      path: clips/
      length: 15
      bitrate: ""
`
			settings, configPath := loadConfigFromYAML(t, cfg)

			assert.Equal(t, DefaultAudioExportBitrate, settings.Realtime.Audio.Export.Bitrate,
				"the loaded settings must carry the documented default bitrate")

			persisted := readPersistedSettings(t, configPath)
			assert.Equal(t, DefaultAudioExportBitrate, persisted.Realtime.Audio.Export.Bitrate,
				"the healed bitrate must be written back to disk so the warning cannot recur")

			// A second load of the healed file must not re-warn about the bitrate.
			viper.Reset()
			ConfigPath = configPath
			reloaded, err := Load()
			require.NoError(t, err)
			assert.NotContains(t, warningsText(reloaded), "no bitrate is set",
				"a healed config must not warn about the bitrate on the next load")
		})
	}
}

// The self-heal write-back runs before the incomplete-feature normalization that
// disables switched-on-but-unconfigured integrations in memory. Persisting the
// whole normalized struct would silently flip a user's enabled integration to
// disabled on disk, which the security rule in validate_incomplete.go forbids. This
// guards that the bitrate write-back preserves an enabled-but-incomplete
// integration's on-disk state.
func TestLoad_EmptyLossyExportBitrate_DoesNotPersistFeatureDisable(t *testing.T) {
	cfg := `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
realtime:
  birdweather:
    enabled: true
    id: ""
  audio:
    export:
      enabled: true
      type: aac
      path: clips/
      length: 15
      bitrate: ""
`
	settings, configPath := loadConfigFromYAML(t, cfg)

	// In memory the incomplete BirdWeather integration is switched off.
	assert.False(t, settings.Realtime.Birdweather.Enabled,
		"an incomplete BirdWeather integration is disabled in memory")

	persisted := readPersistedSettings(t, configPath)
	assert.True(t, persisted.Realtime.Birdweather.Enabled,
		"the write-back must NOT persist the in-memory integration disable to disk")
	assert.Equal(t, DefaultAudioExportBitrate, persisted.Realtime.Audio.Export.Bitrate,
		"the bitrate is still healed on disk")
}

// The migration contract: fill a blank lossy bitrate (returning true), warn only
// when the export is enabled, and leave non-lossy or already-set bitrates alone.
func TestMigrateEmptyLossyExportBitrate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		exportType  string
		bitrate     string
		enabled     bool
		wantChanged bool
		wantBitrate string
		wantWarning bool
	}{
		{"enabled aac blank fills and warns", AudioExportTypeAAC, "", true, true, DefaultAudioExportBitrate, true},
		{"disabled opus blank fills without warning", AudioExportTypeOPUS, "", false, true, DefaultAudioExportBitrate, false},
		{"enabled mp3 blank fills and warns", AudioExportTypeMP3, "", true, true, DefaultAudioExportBitrate, true},
		{"lossless wav blank is left alone", AudioExportTypeWAV, "", true, false, "", false},
		{"already-set bitrate is left alone", AudioExportTypeAAC, "128k", true, false, "128k", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{}
			s.Realtime.Audio.Export.Type = tt.exportType
			s.Realtime.Audio.Export.Bitrate = tt.bitrate
			s.Realtime.Audio.Export.Enabled = tt.enabled

			changed := s.migrateEmptyLossyExportBitrate()

			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, tt.wantBitrate, s.Realtime.Audio.Export.Bitrate)
			if tt.wantWarning {
				assert.Contains(t, warningsText(s), "no bitrate is set")
			} else {
				assert.Empty(t, s.ValidationWarnings)
			}
		})
	}
}

// omitempty on the Bitrate YAML tag stops a save made while the export is empty
// from writing bitrate: "" back, which would permanently defeat the viper default
// on the next load. An omitted key lets the default reapply.
func TestSaveYAMLConfig_OmitsEmptyLossyBitrate(t *testing.T) {
	s := &Settings{}
	s.Realtime.Audio.Export.Type = AudioExportTypeAAC
	s.Realtime.Audio.Export.Bitrate = ""

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveYAMLConfig(configPath, s))

	data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.NotContains(t, string(data), `bitrate: ""`,
		"an empty bitrate must be omitted, not written as an empty string")
}
