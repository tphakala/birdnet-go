package v2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
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
	f, err := os.Create(legacyPath) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f.Close())

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
	f, err := os.Create(v2Path) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f.Close())

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

	f1, err := os.Create(legacyPath) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f1.Close())

	f2, err := os.Create(v2Path) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f2.Close())

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

// TestCheckMigrationState_FreshV2Restart tests that an existing fresh v2 database
// is correctly detected on restart (no birdnet_v2.db, but configured path has v2 schema).
func TestCheckMigrationState_FreshV2Restart_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	// Create a fresh v2 database at the configured path
	manager, err := NewSQLiteManager(Config{DirectPath: configuredPath})
	require.NoError(t, err)
	err = manager.Initialize()
	require.NoError(t, err)

	// Set migration state to COMPLETED (simulates a successful fresh install)
	now := time.Now()
	state := entities.MigrationState{
		ID:          1,
		State:       entities.MigrationStatusCompleted,
		StartedAt:   &now,
		CompletedAt: &now,
	}
	err = manager.DB().Save(&state).Error
	require.NoError(t, err)
	require.NoError(t, manager.Close())

	// Now check migration state - should detect this as v2-ready
	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	startupState := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, startupState.FreshInstall, "should NOT be fresh install on restart")
	assert.True(t, startupState.V2Available, "v2 should be available")
	assert.False(t, startupState.LegacyRequired, "should not require legacy")
	assert.NoError(t, startupState.Error)
}

// TestCheckSQLiteHasV2Schema tests the helper function that distinguishes v2 from legacy databases.
func TestCheckSQLiteHasV2Schema(t *testing.T) {
	t.Run("v2 database", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "v2.db")

		// Create a v2 database
		manager, err := NewSQLiteManager(Config{DirectPath: dbPath})
		require.NoError(t, err)
		err = manager.Initialize()
		require.NoError(t, err)

		// Set migration state to COMPLETED (checkSQLiteHasV2Schema requires this)
		now := time.Now()
		state := entities.MigrationState{
			ID:          1,
			State:       entities.MigrationStatusCompleted,
			StartedAt:   &now,
			CompletedAt: &now,
		}
		err = manager.DB().Save(&state).Error
		require.NoError(t, err)
		require.NoError(t, manager.Close())

		assert.True(t, checkSQLiteHasV2Schema(dbPath), "should detect v2 schema")
	})

	t.Run("non-existent database", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "nonexistent.db")

		assert.False(t, checkSQLiteHasV2Schema(dbPath), "should return false for non-existent")
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "empty.db")

		f, err := os.Create(dbPath) //nolint:gosec // Test file path is safe
		require.NoError(t, err)
		require.NoError(t, f.Close())

		assert.False(t, checkSQLiteHasV2Schema(dbPath), "should return false for empty file")
	})
}
