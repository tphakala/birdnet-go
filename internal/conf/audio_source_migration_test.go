package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateAudioSourceConfig_BasicMigration(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Source = testAudioDeviceSysdefault
	settings.Realtime.Audio.QuietHours = QuietHoursConfig{
		Enabled:   true,
		Mode:      "fixed",
		StartTime: "22:00",
		EndTime:   "06:00",
	}

	migrated := settings.MigrateAudioSourceConfig()

	require.True(t, migrated, "should report migration occurred")
	require.Len(t, settings.Realtime.Audio.Sources, 1)

	src := settings.Realtime.Audio.Sources[0]
	assert.Equal(t, "Sound Card 1", src.Name)
	assert.Equal(t, testAudioDeviceSysdefault, src.Device)
	assert.InDelta(t, 0.0, src.Gain, 0.001)
	assert.Empty(t, src.Model)
	assert.Nil(t, src.Equalizer)
	assert.True(t, src.QuietHours.Enabled, "quiet hours should be carried over")
	assert.Equal(t, "22:00", src.QuietHours.StartTime)
	assert.Equal(t, "06:00", src.QuietHours.EndTime)

	assert.Empty(t, settings.Realtime.Audio.Source, "legacy field should be cleared")
}

func TestMigrateAudioSourceConfig_SkipIfAlreadyMigrated(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{{
		Name:   "My Mic",
		Device: "hw:0,0",
	}}
	settings.Realtime.Audio.Source = testAudioDeviceSysdefault // Leftover legacy, should be ignored

	migrated := settings.MigrateAudioSourceConfig()

	assert.False(t, migrated, "should skip if Sources already populated")
	require.Len(t, settings.Realtime.Audio.Sources, 1)
	assert.Equal(t, "My Mic", settings.Realtime.Audio.Sources[0].Name)
}

func TestMigrateAudioSourceConfig_SkipIfNoLegacySource(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Source = ""

	migrated := settings.MigrateAudioSourceConfig()

	assert.False(t, migrated, "should skip if no legacy source")
	assert.Empty(t, settings.Realtime.Audio.Sources)
}

func TestMigrateAudioSourceConfig_SkipWhitespaceOnlySource(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Source = "   "

	migrated := settings.MigrateAudioSourceConfig()

	assert.False(t, migrated, "should skip whitespace-only source")
	assert.Empty(t, settings.Realtime.Audio.Sources)
}

func TestMigrateAudioSourceConfig_TrimsWhitespace(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Source = "  Loopback  "

	migrated := settings.MigrateAudioSourceConfig()

	require.True(t, migrated)
	require.Len(t, settings.Realtime.Audio.Sources, 1)
	assert.Equal(t, "Loopback", settings.Realtime.Audio.Sources[0].Device)
}

func TestMigrateAudioSourceConfig_Idempotent(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Realtime.Audio.Source = "hw:1,0"

	// First migration
	require.True(t, settings.MigrateAudioSourceConfig())
	require.Len(t, settings.Realtime.Audio.Sources, 1)
	assert.Equal(t, "hw:1,0", settings.Realtime.Audio.Sources[0].Device)

	// Second migration should be a no-op
	assert.False(t, settings.MigrateAudioSourceConfig())
	require.Len(t, settings.Realtime.Audio.Sources, 1)
}
