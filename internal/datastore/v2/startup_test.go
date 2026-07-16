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

func TestCheckMigrationState_FreshInstall_WarnsAboutNearbyDBFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a .db file that is NOT the configured path
	existingDB := filepath.Join(tmpDir, "old-birdnet.db")
	require.NoError(t, os.WriteFile(existingDB, make([]byte, 1024), 0o644))

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(tmpDir, "birdnet.db")

	state := CheckMigrationStateBeforeStartup(settings)

	// Behavior is unchanged: still fresh install
	assert.True(t, state.FreshInstall, "should still detect fresh install")
	assert.False(t, state.LegacyRequired)
	assert.NoError(t, state.Error)
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

// TestCheckMigrationState_V2WithEmptyLegacyResidue_SQLite is a regression test for the
// PR #3926 startup crash: a fully consolidated v2 database (COMPLETED marker, real v2 tables)
// that still carries EMPTY leftover legacy tables ('notes'/'results') from an in-place
// migration must route into v2-only mode. PR #3926 keyed on mere table existence and
// misclassified such healthy databases as contaminated legacy, forcing legacy AutoMigrate,
// which crashed with "Cannot add a NOT NULL column with default value NULL" (GitHub #3924).
func TestCheckMigrationState_V2WithEmptyLegacyResidue_SQLite(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet-2025.db")

	createConsolidatedV2DBAt(t, configuredPath)

	// Reproduce the production residue: empty legacy tables left behind by a completed
	// in-place migration (GORM never drops them).
	db, err := gorm.Open(sqlite.Open(configuredPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// Ensure the writer handle is closed even if a require below fails, so t.TempDir()
	// cleanup does not fail on Windows with an open file handle.
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, db.Exec("CREATE TABLE results (id INTEGER PRIMARY KEY)").Error)

	// Close the writer handle before CheckMigrationStateBeforeStartup opens the file read-only.
	require.NoError(t, sqlDB.Close())

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	startupState := CheckMigrationStateBeforeStartup(settings)

	assert.False(t, startupState.FreshInstall, "should NOT be fresh install")
	assert.True(t, startupState.V2Available, "v2 should be available")
	assert.False(t, startupState.LegacyRequired,
		"legacy must not be required for a completed v2 DB with empty legacy residue")
	assert.Equal(t, entities.MigrationStatusCompleted, startupState.MigrationStatus)
	require.NoError(t, startupState.Error)
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

	// A COMPLETED marker alongside a legacy data table that STILL HOLDS ROWS is PR #2165
	// contamination: real user data was never migrated (GitHub #3924). Each populated legacy
	// table must independently force a false result so CheckAndConsolidateAtStartup preserves
	// the real migrated sidecar instead of deleting it.
	for _, lt := range []struct{ name, ddl, insert string }{
		{"results", "CREATE TABLE results (id INTEGER PRIMARY KEY)", "INSERT INTO results (id) VALUES (1)"},
		{"notes", "CREATE TABLE notes (id INTEGER PRIMARY KEY)", "INSERT INTO notes (id) VALUES (1)"},
	} {
		t.Run("contaminated legacy database (PR #2165 bug) - "+lt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "contaminated.db")

			// Create a v2 database
			manager, err := NewSQLiteManager(Config{DirectPath: dbPath})
			require.NoError(t, err)
			require.NoError(t, manager.Initialize())

			// Set migration state to COMPLETED
			now := time.Now()
			state := entities.MigrationState{
				ID:          1,
				State:       entities.MigrationStatusCompleted,
				StartedAt:   &now,
				CompletedAt: &now,
			}
			require.NoError(t, manager.DB().Save(&state).Error)

			// Simulate PR #2165 contamination: a legacy data table with UNMIGRATED ROWS
			// alongside the COMPLETED marker.
			require.NoError(t, manager.DB().Exec(lt.ddl).Error)
			require.NoError(t, manager.DB().Exec(lt.insert).Error)

			require.NoError(t, manager.Close())

			assert.False(t, CheckSQLiteHasV2Schema(dbPath),
				"should return false when legacy %s table holds unmigrated rows", lt.name)
		})
	}

	// Regression test for PR #3926: an EMPTY leftover legacy table ('results' or 'notes') is
	// harmless residue of a completed in-place migration (GORM never drops tables). The database
	// is a genuine completed v2 schema and must still resolve as v2; the original "table exists"
	// guard wrongly forced these into legacy mode, crashing AutoMigrate on startup (GitHub #3924).
	for _, lt := range []struct{ name, ddl string }{
		{"results", "CREATE TABLE results (id INTEGER PRIMARY KEY)"},
		{"notes", "CREATE TABLE notes (id INTEGER PRIMARY KEY)"},
	} {
		t.Run("completed v2 with empty leftover legacy table - "+lt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "residue.db")

			manager, err := NewSQLiteManager(Config{DirectPath: dbPath})
			require.NoError(t, err)
			require.NoError(t, manager.Initialize())

			now := time.Now()
			state := entities.MigrationState{
				ID:          1,
				State:       entities.MigrationStatusCompleted,
				StartedAt:   &now,
				CompletedAt: &now,
			}
			require.NoError(t, manager.DB().Save(&state).Error)

			// Empty legacy table: residue of a completed migration, not contamination.
			require.NoError(t, manager.DB().Exec(lt.ddl).Error)

			require.NoError(t, manager.Close())

			assert.True(t, CheckSQLiteHasV2Schema(dbPath),
				"an empty leftover legacy %s table must still resolve as v2", lt.name)
		})
	}
}

// newSchemaCheckDB opens a fresh temp-file SQLite GORM DB for hasCompleteFreshV2Schema
// tests. SQLite stands in for MySQL here: hasCompleteFreshV2Schema is dialect-agnostic
// (it uses GORM's migrator), so the same decision logic runs in CI without a MySQL server.
func newSchemaCheckDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "schema_check.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// seedMigrationStatesTable creates the plural migration_states table and inserts a
// singleton row (id=1) with the given state.
func seedMigrationStatesTable(t *testing.T, db *gorm.DB, status entities.MigrationStatus) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&entities.MigrationState{}))
	now := time.Now()
	require.NoError(t, db.Save(&entities.MigrationState{
		ID:          1,
		State:       status,
		StartedAt:   &now,
		CompletedAt: &now,
	}).Error)
}

// createBareDetectionsTable creates a placeholder no-prefix detections table. Only its
// existence matters to hasCompleteFreshV2Schema, so a minimal schema is sufficient.
func createBareDetectionsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE detections (id INTEGER PRIMARY KEY)").Error)
}

// TestHasCompleteFreshV2Schema covers the GitHub #3575 guard: a "completed"
// migration_states marker is treated as a usable fresh v2 schema only when the real
// detections data table also exists.
func TestHasCompleteFreshV2Schema(t *testing.T) {
	t.Run("completed marker with detections table", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		seedMigrationStatesTable(t, db, entities.MigrationStatusCompleted)
		createBareDetectionsTable(t, db)

		assert.True(t, hasCompleteFreshV2Schema(db),
			"completed marker plus detections table is a usable fresh v2 schema")
	})

	t.Run("completed marker without detections table", func(t *testing.T) {
		// This is the #3575 wedge: a stale completed marker survives a backend switch
		// or partial init, but the detections data table was never created.
		db := newSchemaCheckDB(t)
		seedMigrationStatesTable(t, db, entities.MigrationStatusCompleted)

		assert.False(t, hasCompleteFreshV2Schema(db),
			"completed marker without a detections table must not be treated as fresh v2")
	})

	t.Run("detections table but state not completed", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		seedMigrationStatesTable(t, db, entities.MigrationStatusIdle)
		createBareDetectionsTable(t, db)

		assert.False(t, hasCompleteFreshV2Schema(db),
			"a non-completed migration state is not a finished v2 install")
	})

	t.Run("no migration_states table", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		createBareDetectionsTable(t, db)

		assert.False(t, hasCompleteFreshV2Schema(db),
			"missing migration state table means no v2 schema")
	})

	t.Run("migration_states table exists but is empty", func(t *testing.T) {
		// Exercises the First() ErrRecordNotFound branch: the table was created but
		// the singleton state row was never written.
		db := newSchemaCheckDB(t)
		require.NoError(t, db.AutoMigrate(&entities.MigrationState{}))
		createBareDetectionsTable(t, db)

		assert.False(t, hasCompleteFreshV2Schema(db),
			"an empty migration_states table (no state row) is not a finished v2 install")
	})

	t.Run("legacy singular migration_state table name", func(t *testing.T) {
		// Pre-PR #2165 databases used the singular "migration_state" table name.
		db := newSchemaCheckDB(t)
		require.NoError(t, db.Exec("CREATE TABLE migration_state (id INTEGER PRIMARY KEY, state TEXT)").Error)
		require.NoError(t, db.Exec("INSERT INTO migration_state (id, state) VALUES (1, ?)",
			string(entities.MigrationStatusCompleted)).Error)
		createBareDetectionsTable(t, db)

		assert.True(t, hasCompleteFreshV2Schema(db),
			"legacy singular table name must still resolve")
	})

	// The MySQL/dialect-agnostic path must reject either legacy data table ('results' or
	// 'notes') when it STILL HOLDS ROWS (unmigrated data) alongside a COMPLETED marker and a
	// bare detections table.
	for _, lt := range []struct{ name, ddl, insert string }{
		{"results", "CREATE TABLE results (id INTEGER PRIMARY KEY)", "INSERT INTO results (id) VALUES (1)"},
		{"notes", "CREATE TABLE notes (id INTEGER PRIMARY KEY)", "INSERT INTO notes (id) VALUES (1)"},
	} {
		t.Run("contaminated legacy database (PR #2165 bug) - "+lt.name, func(t *testing.T) {
			db := newSchemaCheckDB(t)
			seedMigrationStatesTable(t, db, entities.MigrationStatusCompleted)
			createBareDetectionsTable(t, db)

			// Simulate PR #2165 contamination: a legacy data table with UNMIGRATED ROWS
			// alongside the COMPLETED marker.
			require.NoError(t, db.Exec(lt.ddl).Error)
			require.NoError(t, db.Exec(lt.insert).Error)

			assert.False(t, hasCompleteFreshV2Schema(db),
				"should return false when legacy %s table holds unmigrated rows", lt.name)
		})
	}

	// Regression test for PR #3926: an EMPTY leftover legacy table is harmless residue of a
	// completed in-place migration and must NOT disqualify an otherwise complete fresh v2 schema.
	for _, lt := range []struct{ name, ddl string }{
		{"results", "CREATE TABLE results (id INTEGER PRIMARY KEY)"},
		{"notes", "CREATE TABLE notes (id INTEGER PRIMARY KEY)"},
	} {
		t.Run("completed v2 with empty leftover legacy table - "+lt.name, func(t *testing.T) {
			db := newSchemaCheckDB(t)
			seedMigrationStatesTable(t, db, entities.MigrationStatusCompleted)
			createBareDetectionsTable(t, db)

			// Empty legacy table: residue of a completed migration, not contamination.
			require.NoError(t, db.Exec(lt.ddl).Error)

			assert.True(t, hasCompleteFreshV2Schema(db),
				"an empty leftover legacy %s table must not disqualify a fresh v2 schema", lt.name)
		})
	}
}

// TestLegacyDataPresent locks in the row-count discriminator directly: a legacy table signals
// contamination only when it actually holds rows, and the probe walks past an empty table to
// check the next one (order is results, then notes).
func TestLegacyDataPresent(t *testing.T) {
	t.Run("no legacy tables", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		present, err := legacyDataPresent(db)
		require.NoError(t, err)
		assert.False(t, present, "a database with no legacy tables has no legacy data")
	})

	t.Run("empty legacy tables", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		require.NoError(t, db.Exec("CREATE TABLE results (id INTEGER PRIMARY KEY)").Error)
		require.NoError(t, db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY)").Error)
		present, err := legacyDataPresent(db)
		require.NoError(t, err)
		assert.False(t, present, "empty legacy tables are harmless residue, not contamination")
	})

	t.Run("populated notes", func(t *testing.T) {
		db := newSchemaCheckDB(t)
		require.NoError(t, db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY)").Error)
		require.NoError(t, db.Exec("INSERT INTO notes (id) VALUES (1)").Error)
		present, err := legacyDataPresent(db)
		require.NoError(t, err)
		assert.True(t, present, "a populated legacy notes table is unmigrated data")
	})

	t.Run("empty results but populated notes", func(t *testing.T) {
		// results is probed first and is empty; the loop must continue and still detect
		// the unmigrated rows in notes.
		db := newSchemaCheckDB(t)
		require.NoError(t, db.Exec("CREATE TABLE results (id INTEGER PRIMARY KEY)").Error)
		require.NoError(t, db.Exec("CREATE TABLE notes (id INTEGER PRIMARY KEY)").Error)
		require.NoError(t, db.Exec("INSERT INTO notes (id) VALUES (1)").Error)
		present, err := legacyDataPresent(db)
		require.NoError(t, err)
		assert.True(t, present, "contamination in the second legacy table must still be detected")
	})

	t.Run("metadata probe error fails closed", func(t *testing.T) {
		// A failure to enumerate tables must propagate as an error (fail closed), never be
		// reported as (false, nil) which callers would read as "clean v2". Close the underlying
		// connection to force the GetTables metadata query to fail.
		db := newSchemaCheckDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())

		present, err := legacyDataPresent(db)
		require.Error(t, err, "a metadata-probe failure must propagate")
		assert.False(t, present, "the bool must be the safe zero value on error")
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

// mysqlTestSettings builds conf.Settings from a MySQLConfig for startup tests.
func mysqlTestSettings(cfg *MySQLConfig) *conf.Settings {
	settings := &conf.Settings{}
	settings.Output.MySQL.Enabled = true
	settings.Output.MySQL.Host = cfg.Host
	settings.Output.MySQL.Port = cfg.Port
	settings.Output.MySQL.Username = cfg.Username
	settings.Output.MySQL.Password = cfg.Password
	settings.Output.MySQL.Database = cfg.Database
	return settings
}

// TestCheckMySQLMigrationState_NativePasswordAuth verifies that the MySQL
// startup check works with mysql_native_password authentication, which is the
// default for MariaDB and some MySQL configurations.
func TestCheckMySQLMigrationState_NativePasswordAuth(t *testing.T) {
	settings := mysqlTestSettings(skipIfNoMySQL(t))

	state := checkMySQLMigrationState(settings)

	// Before the fix, mysql_native_password servers would get
	// ErrV2DatabaseCorrupted because AllowNativePasswords defaulted to false.
	require.NoError(t, state.Error, "startup check must not fail due to auth plugin rejection")
	assert.NotEmpty(t, string(state.MigrationStatus), "migration status should be set")
}

// TestHasUnmigratedLegacyMySQL_NativePasswordAuth verifies that the unmigrated
// records check connects successfully with mysql_native_password.
func TestHasUnmigratedLegacyMySQL_NativePasswordAuth(t *testing.T) {
	settings := mysqlTestSettings(skipIfNoMySQL(t))

	// Should not panic or fail due to auth plugin rejection.
	assert.NotPanics(t, func() {
		_ = hasUnmigratedLegacyMySQL(settings, testStartupLogger())
	})
}

// TestCheckMySQLHasFreshV2Schema_NativePasswordAuth verifies that the fresh v2
// schema check connects successfully with mysql_native_password.
func TestCheckMySQLHasFreshV2Schema_NativePasswordAuth(t *testing.T) {
	settings := mysqlTestSettings(skipIfNoMySQL(t))

	assert.NotPanics(t, func() {
		_ = CheckMySQLHasFreshV2Schema(settings)
	})
}

// TestCheckMySQLHasFreshV2Schema_RequiresDetectionsTable reproduces the GitHub #3575
// wedge against a real MySQL server: a no-prefix migration_states "completed" marker
// must NOT be treated as a usable fresh v2 schema when the detections data table is
// missing. Otherwise initializeV2OnlyMode picks useV2Prefix=false and every v2 query
// targets a bare `detections` table that does not exist.
func TestCheckMySQLHasFreshV2Schema_RequiresDetectionsTable(t *testing.T) {
	cfg := skipIfNoMySQL(t)
	settings := mysqlTestSettings(cfg)

	// Build a healthy fresh (no v2_ prefix) v2 schema. cfg.UseV2Prefix is false, so
	// NewMySQLManager creates clean, no-prefix table names.
	mgr, err := NewMySQLManager(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})
	require.NoError(t, mgr.Initialize())
	require.NoError(t, mgr.DB().Model(&entities.MigrationState{}).
		Where("id = 1").
		Update("state", entities.MigrationStatusCompleted).Error)

	// Healthy: completed marker AND detections table present.
	assert.True(t, CheckMySQLHasFreshV2Schema(settings),
		"a completed marker with a detections table is a usable fresh v2 schema")

	// #3575: drop the detections data table but leave the completed marker behind.
	require.NoError(t, mgr.DB().Migrator().DropTable("detections"))
	assert.False(t, CheckMySQLHasFreshV2Schema(settings),
		"a stale completed marker without a detections table must not be treated as fresh v2")
}
