package v2

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
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

// TestCheckMigrationState_StaleEmptySidecar_V2Consolidated is a regression
// test for the production failure where an empty birdnet-2025_v2.db sidecar
// (created by an external deploy script, not by BirdNET-Go) caused startup
// to misroute into legacy mode on a consolidated v2 database, triggering
// "Cannot add a NOT NULL column with default value NULL" from AutoMigrate.
//
// With the fix, an empty/corrupt sidecar next to a healthy v2 database must
// fall through to the configured-path check, return v2-only mode, and remove
// the stale sidecar so subsequent startups take the clean path.
func TestCheckMigrationState_StaleEmptySidecar_V2Consolidated(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet-2025.db")
	v2SidecarPath := filepath.Join(tmpDir, "birdnet-2025_v2.db")

	createConsolidatedV2DBAt(t, configuredPath)

	// Simulate the production fault: a 0-byte sidecar created externally
	f, err := os.Create(v2SidecarPath) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f.Close())

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	startupState := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, startupState.FreshInstall, "should NOT be fresh install")
	assert.True(t, startupState.V2Available, "v2 should be available")
	assert.False(t, startupState.LegacyRequired, "legacy must not be required")
	assert.Equal(t, entities.MigrationStatusCompleted, startupState.MigrationStatus)
	require.NoError(t, startupState.Error)

	// Stale sidecar must be cleaned up so next startup does not re-enter
	// this branch; regression guard for the deploy-script failure mode.
	_, err = os.Stat(v2SidecarPath)
	assert.True(t, os.IsNotExist(err), "stale sidecar should have been removed")
}

// TestCheckMigrationState_EmptySidecar_NoConfigured covers the edge case
// where a stray empty sidecar exists but the configured path is missing.
// The fallback must not trigger (there is no v2 schema to fall back to)
// and startup must surface the corruption error.
func TestCheckMigrationState_EmptySidecar_NoConfigured(t *testing.T) {
	tmpDir := t.TempDir()
	v2SidecarPath := filepath.Join(tmpDir, "birdnet_v2.db")

	f, err := os.Create(v2SidecarPath) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f.Close())

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "birdnet.db")

	startupState := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, startupState.FreshInstall)
	require.Error(t, startupState.Error, "should surface error when no fallback is available")

	// Sidecar must remain: the function only removes it when a valid
	// consolidated v2 database exists at the configured path.
	_, err = os.Stat(v2SidecarPath)
	require.NoError(t, err, "sidecar should be preserved when no v2 fallback exists")
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

		// Set migration state to COMPLETED (CheckSQLiteHasV2Schema requires this)
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

		assert.True(t, CheckSQLiteHasV2Schema(dbPath), "should detect v2 schema")
	})

	t.Run("non-existent database", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "nonexistent.db")

		assert.False(t, CheckSQLiteHasV2Schema(dbPath), "should return false for non-existent")

		// CRITICAL: Verify the file was NOT created during the check.
		// This is a regression test for the bug where GORM/SQLite would create
		// an empty file even when opened with mode=ro, causing the legacy
		// database cleanup UI to appear after the legacy DB was manually deleted.
		_, err := os.Stat(dbPath)
		assert.True(t, os.IsNotExist(err), "checking non-existent file should not create it")
	})

	t.Run("empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "empty.db")

		f, err := os.Create(dbPath) //nolint:gosec // Test file path is safe
		require.NoError(t, err)
		require.NoError(t, f.Close())

		assert.False(t, CheckSQLiteHasV2Schema(dbPath), "should return false for empty file")
	})
}

// createLegacySQLite creates a minimal legacy SQLite database with a notes table
// containing the specified number of records starting from ID 1.
func createLegacySQLite(t *testing.T, path string, recordCount int) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, source_node TEXT)").Error)
	for i := range recordCount {
		require.NoError(t, db.Exec("INSERT INTO notes (id, source_node) VALUES (?, 'test')", i+1).Error)
	}
}

// createConsolidatedV2DBAt creates a fully initialized v2 database at the
// exact path given (not the derived migration sidecar path), with migration
// state set to COMPLETED. Used by regression tests that need a "consolidated
// v2 DB at the configured path" fixture.
func createConsolidatedV2DBAt(t *testing.T, path string) {
	t.Helper()
	mgr, err := NewSQLiteManager(Config{DirectPath: path})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).
		Where("id = 1").
		Update("state", entities.MigrationStatusCompleted).Error)
	require.NoError(t, mgr.Close())
}

// createCompletedV2MigrationDB creates a v2 migration database with COMPLETED state
// and the given LastMigratedID.
func createCompletedV2MigrationDB(t *testing.T, dir string, lastMigratedID uint) {
	t.Helper()
	mgr, err := NewSQLiteManager(Config{DataDir: dir})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	sm := NewStateManager(mgr.DB())
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())

	// Set LastMigratedID to the desired watermark
	if lastMigratedID > 0 {
		require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).
			Where("id = 1").
			Update("last_migrated_id", lastMigratedID).Error)
	}

	require.NoError(t, sm.TransitionToValidating())
	require.NoError(t, sm.TransitionToCutover())
	require.NoError(t, sm.Complete())
	require.NoError(t, mgr.Close())
}

func TestHasUnmigratedLegacyRecords_StragglerFound(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	// Create legacy DB with 15 records
	createLegacySQLite(t, legacyPath, 15)

	// Create v2 migration DB with LastMigratedID=10 (5 stragglers)
	createCompletedV2MigrationDB(t, tmpDir, 10)

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.True(t, hasUnmigrated, "should detect 5 unmigrated records")
}

func TestHasUnmigratedLegacyRecords_NoStragglers(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	// Create legacy DB with 10 records
	createLegacySQLite(t, legacyPath, 10)

	// Create v2 migration DB with LastMigratedID=10 (all migrated)
	createCompletedV2MigrationDB(t, tmpDir, 10)

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.False(t, hasUnmigrated, "should not detect stragglers when all migrated")
}

func TestHasUnmigratedLegacyRecords_NoV2Database(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	// Create legacy DB but no v2 migration DB
	createLegacySQLite(t, legacyPath, 10)

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.False(t, hasUnmigrated, "should return false when no v2 database")
}

func TestHasUnmigratedLegacyRecords_NoLegacyDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	// Create v2 migration DB but no legacy DB
	createCompletedV2MigrationDB(t, tmpDir, 10)

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.False(t, hasUnmigrated, "should return false when no legacy database")
}

func TestHasUnmigratedLegacyRecords_MigrationNotCompleted(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	// Create legacy DB with records
	createLegacySQLite(t, legacyPath, 15)

	// Create v2 migration DB in MIGRATING state (not completed)
	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	sm := NewStateManager(mgr.DB())
	require.NoError(t, sm.StartMigration(10))
	require.NoError(t, sm.TransitionToDualWrite())
	require.NoError(t, sm.TransitionToMigrating())
	require.NoError(t, mgr.Close())

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.False(t, hasUnmigrated, "should return false when migration not completed")
}

func TestHasUnmigratedLegacyRecords_NeitherDBEnabled(t *testing.T) {
	settings := &conf.Settings{}
	// Neither SQLite nor MySQL enabled

	log := testStartupLogger()
	hasUnmigrated := HasUnmigratedLegacyRecords(settings, log)
	assert.False(t, hasUnmigrated, "should return false when no DB enabled")
}

// testStartupLogger returns a silent logger for startup tests.
func testStartupLogger() logger.Logger {
	return logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
}

func TestReportStartupError_NilSafe(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		reportStartupError("sqlite", "openDatabase", fmt.Errorf("database is locked"))
		reportStartupError("sqlite", "openV2Database", fmt.Errorf("open /home/user/birdnet_v2.db: denied"), "/home/user/birdnet_v2.db")
	})
}
