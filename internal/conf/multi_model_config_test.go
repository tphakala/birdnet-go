package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKnownIDs mirrors classifier.KnownConfigIDs() for testing without circular imports.
var testKnownIDs = map[string]bool{"birdnet": true, "birdnet_v3.0": true, "perch_v2": true, "bat": true, "bsg": true}

func TestPerchConfig_Defaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
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

func TestValidateModelConfig_NoErrorsWithJustBirdNET(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet"}
	errs := settings.ValidateModelConfig(testKnownIDs, true)
	assert.Empty(t, errs, "should have no errors with just BirdNET")
}

func TestValidateModelConfig_UnknownModelWarning(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "unknown_model"}
	warnings := settings.ValidateModelConfig(testKnownIDs, true)
	assert.NotEmpty(t, warnings, "unknown model ID should produce a warning")
}

func TestValidateModelConfig_SourceReferencesUnavailableModel(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet"}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0", Models: []string{"birdnet", "perch_v2"}},
	}
	warnings := settings.ValidateModelConfig(testKnownIDs, true)
	assert.NotEmpty(t, warnings, "source referencing model not in models.enabled should warn")
}

func TestValidateModelConfig_SkipSourceRefsAtEarlyLoading(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet"}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0", Models: []string{"birdnet", "perch_v2"}},
	}
	warnings := settings.ValidateModelConfig(testKnownIDs, false)
	assert.Empty(t, warnings, "source reference checks should be skipped when checkSourceRefs is false")
}

func TestApplyModelValidation_AcceptsCatalogAliases(t *testing.T) {
	t.Parallel()

	// A config carrying the hyphenated catalog-style model IDs must validate on
	// the early-load path without an "unknown model ID" warning. Regression for
	// Sentry BIRDNET-GO-2FZ.
	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "perch-v2", "birdnet-v3.0", "birdnet-v2.4"}

	err := settings.applyModelValidation()
	require.NoError(t, err)
	assert.Empty(t, settings.ValidationWarnings, "catalog-style model IDs should not warn")
}

func TestValidAudioModels_AcceptsCatalogAliases(t *testing.T) {
	t.Parallel()

	// A per-source model set to a hyphenated catalog ID must not fail startup.
	assert.True(t, ValidAudioModels["perch-v2"])
	assert.True(t, ValidAudioModels["birdnet-v3.0"])
	assert.True(t, ValidAudioModels["birdnet-v2.4"])
}

func TestMigrateModelIDAliases_CanonicalizesCatalogIDs(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "perch-v2", "birdnet-v3.0"}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Model: "perch-v2", Models: []string{"birdnet-v2.4", "bsg-finland"}},
	}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{URL: "rtsp://x", Models: []string{"perch-v2"}},
	}

	changed := settings.MigrateModelIDAliases()
	require.True(t, changed, "catalog-style IDs should be normalized")

	assert.Equal(t, []string{"birdnet", "perch_v2", "birdnet_v3.0"}, settings.Models.Enabled)
	assert.Equal(t, "perch_v2", settings.Realtime.Audio.Sources[0].Model)
	assert.Equal(t, []string{"birdnet", "bsg"}, settings.Realtime.Audio.Sources[0].Models)
	assert.Equal(t, []string{"perch_v2"}, settings.Realtime.RTSP.Streams[0].Models)

	// Idempotent: a second pass over already-canonical config changes nothing.
	assert.False(t, settings.MigrateModelIDAliases(), "normalized config should be a no-op")
}

func TestMigrateModelIDAliases_CollapsesMixedSpellingDuplicates(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	// Both spellings of the same model, plus a case variant, collapse to one canonical entry.
	settings.Models.Enabled = []string{"perch-v2", "perch_v2", "PERCH-V2", "birdnet"}

	changed := settings.MigrateModelIDAliases()
	require.True(t, changed)
	assert.Equal(t, []string{"perch_v2", "birdnet"}, settings.Models.Enabled)
}

func TestMigrateModelIDAliases_NoChangeForCanonicalConfig(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.Models.Enabled = []string{"birdnet", "perch_v2"}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{{Name: "Mic1", Models: []string{"birdnet"}}}

	assert.False(t, settings.MigrateModelIDAliases(), "already-canonical config must not report a change")
}

func TestBirdNETConfig_VersionField(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.BirdNET.Version = "2.4"
	assert.Equal(t, "2.4", settings.BirdNET.Version)
}
