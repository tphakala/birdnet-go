package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateModelsEnabledAuthoritative covers Part A of the models-enabled-authoritative
// migration (model de-privilege epic, Phase 4): at a config version below 2 the BirdNET
// v2.4 family is moved (or prepended) to the front of models.enabled so config-order
// loading reproduces the legacy v2.4-first order, then the version is stamped to 2. At or
// above version 2 nothing runs, so a deliberate N=0 or non-v2.4-first config is preserved.
func TestMigrateModelsEnabledAuthoritative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     int
		enabled     []string
		wantEnabled []string
		wantChanged bool
	}{
		{
			name:        "absent prepends birdnet",
			version:     0,
			enabled:     []string{"perch_v2"},
			wantEnabled: []string{"birdnet", "perch_v2"},
			wantChanged: true,
		},
		{
			name:        "explicit empty list prepends birdnet (it loaded v2.4 implicitly)",
			version:     0,
			enabled:     []string{},
			wantEnabled: []string{"birdnet"},
			wantChanged: true,
		},
		{
			name:        "nil list prepends birdnet",
			version:     1,
			enabled:     nil,
			wantEnabled: []string{"birdnet"},
			wantChanged: true,
		},
		{
			name:        "already first stays put, only the version stamps",
			version:     1,
			enabled:     []string{"birdnet", "perch_v2"},
			wantEnabled: []string{"birdnet", "perch_v2"},
			wantChanged: true,
		},
		{
			name:        "present but not first is moved to the front",
			version:     1,
			enabled:     []string{"perch_v2", "birdnet"},
			wantEnabled: []string{"birdnet", "perch_v2"},
			wantChanged: true,
		},
		{
			name:        "present in the middle is moved to the front",
			version:     0,
			enabled:     []string{"perch_v2", "birdnet", "birdnet_v3.0"},
			wantEnabled: []string{"birdnet", "perch_v2", "birdnet_v3.0"},
			wantChanged: true,
		},
		{
			name:        "catalog spelling is recognized and moved to the front, keeping its spelling",
			version:     0,
			enabled:     []string{"perch_v2", "birdnet-v2.4"},
			wantEnabled: []string{"birdnet-v2.4", "perch_v2"},
			wantChanged: true,
		},
		{
			name:        "duplicate v2.4 spellings: the first is moved to the front, the rest are left",
			version:     0,
			enabled:     []string{"perch_v2", "birdnet", "birdnet-v2.4"},
			wantEnabled: []string{"birdnet", "perch_v2", "birdnet-v2.4"},
			wantChanged: true,
		},
		{
			name:        "already at version 2 with an empty list stays empty (supported N=0)",
			version:     configVersionModelsEnabledAuthoritative,
			enabled:     []string{},
			wantEnabled: []string{},
			wantChanged: false,
		},
		{
			name:        "already at version 2 with a non-v2.4-first list is not reordered",
			version:     configVersionModelsEnabledAuthoritative,
			enabled:     []string{"perch_v2", "birdnet"},
			wantEnabled: []string{"perch_v2", "birdnet"},
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Settings{ConfigVersion: tt.version}
			s.Models.Enabled = tt.enabled

			changed := s.MigrateModelsEnabledAuthoritative()

			assert.Equal(t, tt.wantChanged, changed)
			assert.Equal(t, tt.wantEnabled, s.Models.Enabled)
			if tt.wantChanged {
				assert.Equal(t, configVersionModelsEnabledAuthoritative, s.ConfigVersion,
					"a changed migration stamps the version")
			}
		})
	}
}

// TestMigrateModelsEnabledAuthoritative_NeverTouchesSourceTargets pins that the migration
// only reorders models.enabled and never mutates per-source or per-stream target lists,
// which resolve from the loaded model set independently.
func TestMigrateModelsEnabledAuthoritative_NeverTouchesSourceTargets(t *testing.T) {
	t.Parallel()

	s := &Settings{ConfigVersion: 1}
	s.Models.Enabled = []string{"perch_v2", "birdnet"}
	s.Realtime.Audio.Sources = []AudioSourceConfig{{Name: "front", Models: []string{"perch_v2"}}}
	s.Realtime.RTSP.Streams = []StreamConfig{{Name: "cam", URL: "rtsp://x", Enabled: true, Models: []string{"birdnet_v3.0"}}}

	require.True(t, s.MigrateModelsEnabledAuthoritative())

	assert.Equal(t, []string{"birdnet", "perch_v2"}, s.Models.Enabled)
	require.Len(t, s.Realtime.Audio.Sources, 1)
	assert.Equal(t, []string{"perch_v2"}, s.Realtime.Audio.Sources[0].Models,
		"per-source targets are independent of the models.enabled reorder")
	require.Len(t, s.Realtime.RTSP.Streams, 1)
	assert.Equal(t, []string{"birdnet_v3.0"}, s.Realtime.RTSP.Streams[0].Models,
		"per-stream targets are independent of the models.enabled reorder")
}

// TestIsBirdNETV24ConfigID pins the two BirdNET v2.4 config spellings recognized by the
// migration, case-insensitively, and that unrelated IDs do not match.
func TestIsBirdNETV24ConfigID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		want bool
	}{
		{ModelIDBirdNET, true},
		{ModelIDBirdNETCatalog, true},
		{"BIRDNET", true},
		{"BirdNet-V2.4", true},
		{"perch_v2", false},
		{ModelIDBirdNETV3, false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, isBirdNETV24ConfigID(tt.id), "isBirdNETV24ConfigID(%q)", tt.id)
	}
}

// TestStampConfigVersion pins that a freshly generated config is prefixed with the current
// config version so this build's one-shot migrations never rewrite a file it just created.
func TestStampConfigVersion(t *testing.T) {
	t.Parallel()

	got := stampConfigVersion("debug: false\n")
	assert.Contains(t, got, "configversion:")
	assert.Greater(t, len(got), len("debug: false\n"), "the stamp is prefixed to the body")
	// The stamp must reflect the newest version this build applies.
	require.Equal(t, configVersionModelsEnabledAuthoritative, currentConfigVersion)
}

// TestMigrateModelsEnabledAuthoritative_ThroughLoad is test (g): Part A exercised through the
// full Load pipeline. A pre-v2 config has v2.4 moved (or prepended) to the front of
// models.enabled, is stamped to version 2, and is rewritten to disk so a second load is a
// no-op; a config already at version 2 is left exactly as written, including an explicit
// empty list (the supported N=0 state). Not parallel: Load publishes the global snapshot.
func TestMigrateModelsEnabledAuthoritative_ThroughLoad(t *testing.T) {
	t.Run("pre-v2 config prepends birdnet, stamps version 2, and persists", func(t *testing.T) {
		cfg := "debug: false\nmodels:\n  enabled:\n    - perch_v2\n"
		settings, configPath := loadConfigFromYAML(t, cfg)
		assert.Equal(t, []string{"birdnet", "perch_v2"}, settings.Models.Enabled)
		assert.Equal(t, configVersionModelsEnabledAuthoritative, settings.ConfigVersion)

		persisted := readPersistedSettings(t, configPath)
		assert.Equal(t, []string{"birdnet", "perch_v2"}, persisted.Models.Enabled,
			"the reordered list is written back so a second load is a no-op")
		assert.Equal(t, configVersionModelsEnabledAuthoritative, persisted.ConfigVersion)
	})

	t.Run("pre-v2 config with v2.4 not first moves it to the front", func(t *testing.T) {
		cfg := "debug: false\nmodels:\n  enabled:\n    - perch_v2\n    - birdnet\n"
		settings, _ := loadConfigFromYAML(t, cfg)
		assert.Equal(t, []string{"birdnet", "perch_v2"}, settings.Models.Enabled)
	})

	t.Run("version 2 with an explicit empty list stays empty (N=0)", func(t *testing.T) {
		cfg := "configversion: 2\ndebug: false\nmodels:\n  enabled: []\n"
		settings, _ := loadConfigFromYAML(t, cfg)
		assert.Empty(t, settings.Models.Enabled, "an explicit N=0 config at version 2 is preserved")
		assert.Equal(t, configVersionModelsEnabledAuthoritative, settings.ConfigVersion)
	})
}

// TestCloneSettings_PreservesAutoEnableMigrated is test (h): the one-shot capture marker
// must survive a clone (and therefore a StoreSettings/SaveSettings round-trip), so the
// classifier does not re-run the legacy auto-enable after it has captured once.
func TestCloneSettings_PreservesAutoEnableMigrated(t *testing.T) {
	t.Parallel()

	src := &Settings{}
	src.Models.Enabled = []string{"birdnet", "perch_v2"}
	src.Models.AutoEnableMigrated = true

	dst := CloneSettings(src)
	require.NotNil(t, dst)
	assert.True(t, dst.Models.AutoEnableMigrated, "the capture marker is preserved by CloneSettings")

	// Mutating the clone's enabled slice must not affect the source (defensive clone).
	dst.Models.Enabled[0] = "changed"
	assert.Equal(t, "birdnet", src.Models.Enabled[0], "the enabled slice is cloned, not aliased")
}
