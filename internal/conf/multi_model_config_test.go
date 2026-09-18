package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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

func TestMigrateSourceModels_LeavesEmptyListForDefaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Mic1", Device: "hw:0,0"},
	}
	migrated := settings.MigrateSourceModels()
	assert.False(t, migrated, "an empty source is no longer filled here; that is MigrateSourceTargetDefaults")
	assert.Empty(t, settings.Realtime.Audio.Sources[0].Models, "an empty list now means the default targets")
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

func TestMigrateSourceModels_LeavesStreamsUntouched(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Cam1", URL: "rtsp://host/audio"},
	}
	migrated := settings.MigrateSourceModels()
	assert.False(t, migrated, "streams have no singular Model field, so MigrateSourceModels does not touch them")
	assert.Empty(t, settings.Realtime.RTSP.Streams[0].Models, "an empty stream list now means the default targets")
}

// TestMigrateSourceTargetDefaults covers the one-shot Phase 4 migration that pins a
// pre-Phase-4 empty source or stream model list to ["birdnet"] (the old meaning of
// empty) and stamps ConfigVersion so it never runs twice.
func TestMigrateSourceTargetDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     int
		sources     []AudioSourceConfig
		streams     []StreamConfig
		wantChanged bool
		wantSources [][]string // expected Models per source, in order
		wantStreams [][]string // expected Models per stream, in order
	}{
		{
			name:        "empty source list is pinned to birdnet and stamped",
			sources:     []AudioSourceConfig{{Name: "Mic1"}},
			wantChanged: true,
			wantSources: [][]string{{"birdnet"}},
		},
		{
			name:        "empty stream list is pinned to birdnet",
			streams:     []StreamConfig{{Name: "Cam1", URL: "rtsp://h/a"}},
			wantChanged: true,
			wantStreams: [][]string{{"birdnet"}},
		},
		{
			name:        "non-empty lists are left untouched but the file is stamped",
			sources:     []AudioSourceConfig{{Name: "Mic1", Models: []string{"perch_v2"}}},
			streams:     []StreamConfig{{Name: "Cam1", URL: "rtsp://h/a", Models: []string{"birdnet", "perch_v2"}}},
			wantChanged: true,
			wantSources: [][]string{{"perch_v2"}},
			wantStreams: [][]string{{"birdnet", "perch_v2"}},
		},
		{
			name:        "an already-migrated file keeps its empty lists and does not run again",
			version:     configVersionSourceTargetDefaults,
			sources:     []AudioSourceConfig{{Name: "Mic1"}},
			streams:     []StreamConfig{{Name: "Cam1", URL: "rtsp://h/a"}},
			wantChanged: false,
			wantSources: [][]string{nil},
			wantStreams: [][]string{nil},
		},
		{
			name:        "no sources or streams still stamps the version once",
			wantChanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{ConfigVersion: tt.version}
			settings.Realtime.Audio.Sources = tt.sources
			settings.Realtime.RTSP.Streams = tt.streams

			changed := settings.MigrateSourceTargetDefaults()
			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, configVersionSourceTargetDefaults, settings.ConfigVersion,
				"the version is stamped whether or not a list was filled")
			for i := range tt.wantSources {
				assert.Equal(t, tt.wantSources[i], settings.Realtime.Audio.Sources[i].Models, "source %d models", i)
			}
			for i := range tt.wantStreams {
				assert.Equal(t, tt.wantStreams[i], settings.Realtime.RTSP.Streams[i].Models, "stream %d models", i)
			}

			// Idempotent: a second call never changes anything again.
			assert.False(t, settings.MigrateSourceTargetDefaults(), "second run is a no-op")
		})
	}
}

// TestMigrateModelsEnabledAuthoritative covers the one-shot Phase 4 migration that
// writes the implicit BirdNET v2.4 enable out to models.enabled (prepended) once and
// stamps ConfigVersion, so every existing install keeps loading the same set.
func TestMigrateModelsEnabledAuthoritative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     int
		enabled     []string
		wantChanged bool
		wantEnabled []string
	}{
		{
			name:        "nil list at version 0 gains birdnet",
			enabled:     nil,
			wantChanged: true,
			wantEnabled: []string{"birdnet"},
		},
		{
			name:        "explicit empty list at version 0 still loaded v2.4 implicitly, so it is pinned",
			enabled:     []string{},
			wantChanged: true,
			wantEnabled: []string{"birdnet"},
		},
		{
			name:        "birdnet is prepended, not appended, ahead of a secondary",
			enabled:     []string{"perch_v2"},
			wantChanged: true,
			wantEnabled: []string{"birdnet", "perch_v2"},
		},
		{
			name:        "a list already naming birdnet is stamped but not changed",
			enabled:     []string{"birdnet", "perch_v2"},
			wantChanged: true,
			wantEnabled: []string{"birdnet", "perch_v2"},
		},
		{
			name:        "the config spelling is matched case-insensitively",
			version:     configVersionSourceTargetDefaults,
			enabled:     []string{"BIRDNET"},
			wantChanged: true,
			wantEnabled: []string{"BIRDNET"},
		},
		{
			name:        "the catalog spelling counts as v2.4 so no duplicate is prepended",
			version:     configVersionSourceTargetDefaults,
			enabled:     []string{"birdnet-v2.4"},
			wantChanged: true,
			wantEnabled: []string{"birdnet-v2.4"},
		},
		{
			name:        "an explicit empty list at version 2 means N=0 and survives untouched",
			version:     configVersionModelsEnabledAuthoritative,
			enabled:     []string{},
			wantChanged: false,
			wantEnabled: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &Settings{ConfigVersion: tt.version}
			settings.Models.Enabled = tt.enabled

			changed := settings.MigrateModelsEnabledAuthoritative()
			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, tt.wantEnabled, settings.Models.Enabled)
			assert.Equal(t, configVersionModelsEnabledAuthoritative, settings.ConfigVersion,
				"the version is stamped whether or not the list changed")

			// Idempotent: a second call never changes anything again.
			assert.False(t, settings.MigrateModelsEnabledAuthoritative(), "second run is a no-op")
		})
	}
}

func TestIsBirdNETV24ConfigID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"birdnet", "BirdNET", "BIRDNET", "birdnet-v2.4", "BirdNET-V2.4"} {
		assert.True(t, isBirdNETV24ConfigID(id), "%q should be recognized as the v2.4 family", id)
	}
	for _, id := range []string{"birdnet_v3.0", "perch_v2", "bat", "", "birdnetv2.4"} {
		assert.False(t, isBirdNETV24ConfigID(id), "%q should not be recognized as v2.4", id)
	}
}

func TestStampConfigVersion(t *testing.T) {
	t.Parallel()
	stamped := stampConfigVersion("debug: false\n")

	var settings Settings
	require.NoError(t, yaml.Unmarshal([]byte(stamped), &settings))
	assert.Equal(t, currentConfigVersion, settings.ConfigVersion,
		"a freshly stamped config carries the current version")
	assert.False(t, settings.MigrateSourceTargetDefaults(), "M1 never runs on a freshly stamped config")
	assert.False(t, settings.MigrateModelsEnabledAuthoritative(), "M2 never runs on a freshly stamped config")
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
