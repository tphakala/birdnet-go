package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateLogDeduplicationDefaults pins the version 3 one-shot repair: only the exact
// zero pair (enabled false, interval 0) that builds without viper defaults wrote is
// restored, and only below the migration's config version.
func TestMigrateLogDeduplicationDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		version      int
		enabled      bool
		interval     int
		wantEnabled  bool
		wantInterval int
		wantChanged  bool
	}{
		{"zero pair is restored", 2, false, 0, true, DefaultLogDeduplicationHealthCheckSeconds, true},
		{"deliberate disable with an interval is kept", 2, false, 30, false, 30, true},
		{"enabled with interval 0 is kept", 2, true, 0, true, 0, true},
		{"zero pair at the current version is kept", configVersionLogDeduplicationDefaults, false, 0, false, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{ConfigVersion: tt.version}
			s.Realtime.LogDeduplication.Enabled = tt.enabled
			s.Realtime.LogDeduplication.HealthCheckIntervalSeconds = tt.interval

			assert.Equal(t, tt.wantChanged, s.MigrateLogDeduplicationDefaults())
			assert.Equal(t, tt.wantEnabled, s.Realtime.LogDeduplication.Enabled)
			assert.Equal(t, tt.wantInterval, s.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
			assert.Equal(t, configVersionLogDeduplicationDefaults, s.ConfigVersion)
		})
	}
}

// TestMigrateLogDeduplicationDefaults_ThroughLoad runs the repair through the full Load
// pipeline. Not parallel: Load publishes the global snapshot.
func TestMigrateLogDeduplicationDefaults_ThroughLoad(t *testing.T) {
	t.Run("pre-v3 zero block is repaired in memory and on disk", func(t *testing.T) {
		cfg := "configversion: 2\ndebug: false\nrealtime:\n  logdeduplication:\n    enabled: false\n    healthcheckintervalseconds: 0\n"
		settings, configPath := loadConfigFromYAML(t, cfg)
		assert.True(t, settings.Realtime.LogDeduplication.Enabled)
		assert.Equal(t, DefaultLogDeduplicationHealthCheckSeconds, settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
		assert.Equal(t, configVersionLogDeduplicationDefaults, settings.ConfigVersion)

		persisted := readPersistedSettings(t, configPath)
		assert.True(t, persisted.Realtime.LogDeduplication.Enabled, "the repair must be written back")
		assert.Equal(t, DefaultLogDeduplicationHealthCheckSeconds, persisted.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
		assert.Equal(t, configVersionLogDeduplicationDefaults, persisted.ConfigVersion)

		viper.Reset()
		ConfigPath = configPath
		reloaded, err := Load()
		require.NoError(t, err)
		assert.True(t, reloaded.Realtime.LogDeduplication.Enabled)
		assert.Equal(t, DefaultLogDeduplicationHealthCheckSeconds, reloaded.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
		assert.Equal(t, configVersionLogDeduplicationDefaults, reloaded.ConfigVersion)
	})

	t.Run("a zero block written after version 3 is kept", func(t *testing.T) {
		cfg := "configversion: 3\ndebug: false\nrealtime:\n  logdeduplication:\n    enabled: false\n    healthcheckintervalseconds: 0\n"
		settings, configPath := loadConfigFromYAML(t, cfg)
		assert.False(t, settings.Realtime.LogDeduplication.Enabled)
		assert.Zero(t, settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds)

		persisted := readPersistedSettings(t, configPath)
		assert.False(t, persisted.Realtime.LogDeduplication.Enabled)
		assert.Zero(t, persisted.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
	})

	t.Run("a disable without the interval key is kept", func(t *testing.T) {
		cfg := "configversion: 2\ndebug: false\nrealtime:\n  logdeduplication:\n    enabled: false\n"
		settings, _ := loadConfigFromYAML(t, cfg)
		assert.False(t, settings.Realtime.LogDeduplication.Enabled,
			"the interval viper default must keep a hand-edited disable out of the zero pair")
		assert.Equal(t, DefaultLogDeduplicationHealthCheckSeconds, settings.Realtime.LogDeduplication.HealthCheckIntervalSeconds)
		assert.Equal(t, configVersionLogDeduplicationDefaults, settings.ConfigVersion)
	})
}
