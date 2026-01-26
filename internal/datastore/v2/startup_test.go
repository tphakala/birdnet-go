package v2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestCheckMigrationState_FreshInstall_SQLite(t *testing.T) {
	// Create temp directory with no database files
	tmpDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "birdnet.db")

	state := CheckMigrationStateBeforeStartup(settings)

	assert.True(t, state.FreshInstall, "should detect fresh install")
	assert.False(t, state.LegacyRequired, "should not require legacy")
	assert.False(t, state.V2Available, "v2 should not be available")
	assert.NoError(t, state.Error)
}

func TestCheckMigrationState_FreshInstall_CustomPath(t *testing.T) {
	// User configured custom database path
	tmpDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "custom", "mybirds.db")

	state := CheckMigrationStateBeforeStartup(settings)

	assert.True(t, state.FreshInstall, "should detect fresh install with custom path")
	assert.False(t, state.LegacyRequired, "should not require legacy")
}

func TestCheckMigrationState_ExistingLegacy_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// Create empty legacy database file
	f, err := os.Create(legacyPath)
	require.NoError(t, err)
	f.Close()

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	state := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, state.FreshInstall, "should NOT be fresh install")
	assert.True(t, state.LegacyRequired, "should require legacy")
	assert.False(t, state.V2Available, "v2 should not be available")
}

func TestCheckMigrationState_V2MigrationDBExists_SQLite(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the v2 migration database file (empty, will fail to read state)
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")
	f, err := os.Create(v2Path)
	require.NoError(t, err)
	f.Close()

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "birdnet.db")

	state := CheckMigrationStateBeforeStartup(settings)

	// Should detect v2 exists but fail to read state (empty file)
	assert.False(t, state.FreshInstall, "should NOT be fresh install when v2 exists")
	// Will have an error because empty file is not a valid SQLite database
	assert.Error(t, state.Error)
}

func TestCheckMigrationState_BothDBsExist_SQLite(t *testing.T) {
	tmpDir := t.TempDir()

	// Create both database files
	legacyPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")

	f1, err := os.Create(legacyPath)
	require.NoError(t, err)
	f1.Close()

	f2, err := os.Create(v2Path)
	require.NoError(t, err)
	f2.Close()

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	state := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, state.FreshInstall, "should NOT be fresh install when both exist")
}

func TestCheckMigrationState_DeepNestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	// Custom path with multiple nested directories that don't exist
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "data", "birds", "detections", "mydb.db")

	state := CheckMigrationStateBeforeStartup(settings)

	assert.True(t, state.FreshInstall, "should detect fresh install with deep nested path")
	assert.False(t, state.LegacyRequired, "should not require legacy")
}
