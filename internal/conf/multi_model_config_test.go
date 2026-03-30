package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKnownIDs mirrors classifier.KnownConfigIDs() for testing without circular imports.
var testKnownIDs = map[string]bool{"birdnet": true, "perch_v2": true}

func TestPerchConfig_Defaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	assert.False(t, settings.Perch.Enabled)
	assert.Empty(t, settings.Perch.ModelPath)
	assert.Empty(t, settings.Perch.LabelPath)
	assert.InDelta(t, 0.0, settings.Perch.Threshold, 0.001)
}

func TestModelsConfig_Defaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	assert.Empty(t, settings.Models.Enabled)
}

func TestAudioSourceConfig_ModelsField(t *testing.T) {
	t.Parallel()
	src := AudioSourceConfig{
		Name:   "Test Mic",
		Device: "hw:0,0",
		Models: []string{"birdnet", "perch_v2"},
	}
	assert.Equal(t, []string{"birdnet", "perch_v2"}, src.Models)
}

func TestStreamConfig_ModelsField(t *testing.T) {
	t.Parallel()
	stream := StreamConfig{
		Name:   "Garden Cam",
		URL:    "rtsp://192.168.1.100/audio",
		Models: []string{"birdnet"},
	}
	assert.Equal(t, []string{"birdnet"}, stream.Models)
}

func TestMigrateSourceModels_SingularToPlural(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0", Model: "perch_v2"},
	}
	migrated := settings.MigrateSourceModels()
	require.True(t, migrated)
	assert.Equal(t, []string{"perch_v2"}, settings.Realtime.Audio.Sources[0].Models)
	assert.Empty(t, settings.Realtime.Audio.Sources[0].Model, "legacy field should be cleared")
}

func TestMigrateSourceModels_DefaultToBirdNET(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0"},
	}
	migrated := settings.MigrateSourceModels()
	require.True(t, migrated)
	assert.Equal(t, []string{"birdnet"}, settings.Realtime.Audio.Sources[0].Models)
}

func TestMigrateSourceModels_SkipIfModelsAlreadySet(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0", Models: []string{"birdnet", "perch_v2"}},
	}
	migrated := settings.MigrateSourceModels()
	assert.False(t, migrated, "should not migrate if Models already set")
	assert.Equal(t, []string{"birdnet", "perch_v2"}, settings.Realtime.Audio.Sources[0].Models)
}

func TestMigrateSourceModels_StreamConfigMigration(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Cam1", URL: "rtsp://host/audio"},
	}
	migrated := settings.MigrateSourceModels()
	require.True(t, migrated)
	assert.Equal(t, []string{"birdnet"}, settings.Realtime.RTSP.Streams[0].Models)
}

func TestValidateModelConfig_PerchEnabledRequiresPaths(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Perch.Enabled = true
	settings.Models.Enabled = []string{"birdnet", "perch_v2"}
	errs := settings.ValidateModelConfig(testKnownIDs)
	assert.NotEmpty(t, errs, "should return errors when Perch enabled without paths")
}

func TestValidateModelConfig_PerchDisabledNoErrors(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet"}
	errs := settings.ValidateModelConfig(testKnownIDs)
	assert.Empty(t, errs, "should have no errors with just BirdNET")
}

func TestValidateModelConfig_PerchInModelsRequiresEnabled(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "perch_v2"}
	settings.Perch.Enabled = false
	errs := settings.ValidateModelConfig(testKnownIDs)
	assert.NotEmpty(t, errs, "perch_v2 in models.enabled requires perch.enabled=true")
}

func TestValidateModelConfig_UnknownModelWarning(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "unknown_model"}
	warnings := settings.ValidateModelConfig(testKnownIDs)
	assert.NotEmpty(t, warnings, "unknown model ID should produce a warning")
}

func TestValidateModelConfig_SourceReferencesUnavailableModel(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet"}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0", Models: []string{"birdnet", "perch_v2"}},
	}
	warnings := settings.ValidateModelConfig(testKnownIDs)
	assert.NotEmpty(t, warnings, "source referencing model not in models.enabled should warn")
}

func TestBirdNETConfig_VersionField(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.BirdNET.Version = "2.4"
	assert.Equal(t, "2.4", settings.BirdNET.Version)
}
