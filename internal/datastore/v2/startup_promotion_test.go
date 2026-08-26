package v2

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openTempIDTable opens a fresh SQLite database at path and creates a single table
// holding only an integer primary key, seeded with the given ids. It is used to unit
// test the tail membership logic without the full detection schema.
func openTempIDTable(t *testing.T, path, table string, ids []uint) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.Exec("CREATE TABLE "+table+" (id INTEGER PRIMARY KEY)").Error)
	for _, id := range ids {
		require.NoError(t, db.Exec("INSERT INTO "+table+" (id) VALUES (?)", id).Error)
	}
	return db
}

// seq returns the inclusive integer range [from, to] as a []uint.
func seq(from, to uint) []uint {
	out := make([]uint, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// insertV2DetectionIDs opens the v2 sidecar at path and inserts detection rows with the
// given ids, filling the NOT NULL columns with dummy values. It opens its own connection
// (foreign keys are off by default on this DSN) so it does not need the referenced label,
// model, or source rows to exist. The v2 Detection.ID equals the legacy notes.id during
// migration, so these ids stand in for dual-written detections already present in v2.
func insertV2DetectionIDs(t *testing.T, path string, ids []uint) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	for _, id := range ids {
		require.NoError(t, db.Exec(
			"INSERT INTO detections (id, model_id, label_id, detected_at, confidence) VALUES (?, 1, 1, ?, 0.5)",
			id, int64(id)).Error)
	}
}

// insertV2DirtyIDs opens the v2 sidecar at path and inserts rows into the
// migration_dirty_ids table, simulating dual-write records the worker has not yet
// reconciled into v2.
func insertV2DirtyIDs(t *testing.T, path string, ids []uint) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer func() { require.NoError(t, sqlDB.Close()) }()

	for _, id := range ids {
		require.NoError(t, db.Exec(
			"INSERT INTO migration_dirty_ids (detection_id, created_at) VALUES (?, 0)", id).Error)
	}
}

// copyFile copies src to dst, truncating dst if it already exists.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // test-controlled path
	require.NoError(t, err)
	defer func() { require.NoError(t, out.Close()) }()
	_, err = io.Copy(out, in)
	require.NoError(t, err)
}

// TestLegacyTailMissingFromV2 unit tests the primary-key membership scan that decides
// whether any legacy tail record is absent from v2. It uses minimal id-only tables so
// the logic (keyset pagination, short-circuit, batch boundaries) is exercised in
// isolation from the full detection schema.
func TestLegacyTailMissingFromV2(t *testing.T) {
	tests := []struct {
		name           string
		notes          []uint
		detections     []uint
		lastMigratedID uint
		wantMissing    bool
	}{
		{
			name:           "all tail records present in v2",
			notes:          seq(1, 15),
			detections:     seq(1, 15),
			lastMigratedID: 10,
			wantMissing:    false,
		},
		{
			name:           "one tail record absent from v2",
			notes:          seq(1, 15),
			detections:     []uint{11, 12, 13, 14}, // 15 missing
			lastMigratedID: 10,
			wantMissing:    true,
		},
		{
			name:           "no tail beyond watermark",
			notes:          seq(1, 10),
			detections:     nil,
			lastMigratedID: 10,
			wantMissing:    false,
		},
		{
			name:           "v2 has only the tail, older records irrelevant",
			notes:          seq(1, 15),
			detections:     []uint{11, 12, 13, 14, 15},
			lastMigratedID: 10,
			wantMissing:    false,
		},
		{
			name:           "multi-batch scan, all present",
			notes:          seq(1, 600),
			detections:     seq(1, 600),
			lastMigratedID: 0,
			wantMissing:    false,
		},
		{
			name:           "multi-batch scan, gap in a later batch",
			notes:          seq(1, 600),
			detections:     append(seq(1, 549), seq(551, 600)...), // 550 missing (second batch)
			lastMigratedID: 0,
			wantMissing:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			legacyDB := openTempIDTable(t, filepath.Join(dir, "legacy.db"), "notes", tt.notes)
			v2DB := openTempIDTable(t, filepath.Join(dir, "v2.db"), "detections", tt.detections)

			missing, err := legacyTailMissingFromV2(legacyDB, v2DB, tt.lastMigratedID, "notes", "detections")
			require.NoError(t, err)
			assert.Equal(t, tt.wantMissing, missing)
		})
	}
}

// TestLegacyTailReconciledInV2 unit tests the combined dirty-ID plus membership decision.
func TestLegacyTailReconciledInV2(t *testing.T) {
	t.Run("all present and no dirty IDs is reconciled", func(t *testing.T) {
		dir := t.TempDir()
		legacyDB := openTempIDTable(t, filepath.Join(dir, "legacy.db"), "notes", seq(1, 15))
		v2DB := openTempIDTable(t, filepath.Join(dir, "v2.db"), "detections", seq(1, 15))
		require.NoError(t, v2DB.Exec("CREATE TABLE migration_dirty_ids (detection_id INTEGER PRIMARY KEY, created_at INTEGER)").Error)

		assert.True(t, legacyTailReconciledInV2(legacyDB, v2DB, 10, "notes", "detections", "migration_dirty_ids", testStartupLogger()))
	})

	t.Run("dirty IDs defer even when tail is present", func(t *testing.T) {
		dir := t.TempDir()
		legacyDB := openTempIDTable(t, filepath.Join(dir, "legacy.db"), "notes", seq(1, 15))
		v2DB := openTempIDTable(t, filepath.Join(dir, "v2.db"), "detections", seq(1, 15))
		require.NoError(t, v2DB.Exec("CREATE TABLE migration_dirty_ids (detection_id INTEGER PRIMARY KEY, created_at INTEGER)").Error)
		require.NoError(t, v2DB.Exec("INSERT INTO migration_dirty_ids (detection_id, created_at) VALUES (7, 0)").Error)

		assert.False(t, legacyTailReconciledInV2(legacyDB, v2DB, 10, "notes", "detections", "migration_dirty_ids", testStartupLogger()))
	})

	t.Run("missing tail record defers", func(t *testing.T) {
		dir := t.TempDir()
		legacyDB := openTempIDTable(t, filepath.Join(dir, "legacy.db"), "notes", seq(1, 15))
		v2DB := openTempIDTable(t, filepath.Join(dir, "v2.db"), "detections", seq(1, 13)) // 14,15 absent
		require.NoError(t, v2DB.Exec("CREATE TABLE migration_dirty_ids (detection_id INTEGER PRIMARY KEY, created_at INTEGER)").Error)

		assert.False(t, legacyTailReconciledInV2(legacyDB, v2DB, 10, "notes", "detections", "migration_dirty_ids", testStartupLogger()))
	})

	t.Run("empty dirty table name skips dirty check", func(t *testing.T) {
		dir := t.TempDir()
		legacyDB := openTempIDTable(t, filepath.Join(dir, "legacy.db"), "notes", seq(1, 15))
		v2DB := openTempIDTable(t, filepath.Join(dir, "v2.db"), "detections", seq(1, 15))

		assert.True(t, legacyTailReconciledInV2(legacyDB, v2DB, 10, "notes", "detections", "", testStartupLogger()))
	})

	// MySQL uses ONE database handle for both the legacy notes and the prefixed
	// v2_detections table (unlike SQLite, which uses two separate files). These
	// cases exercise that single-handle, prefixed-table shape so the MySQL
	// promotion path is not left untested.
	t.Run("MySQL single-handle shape: full tail present is reconciled", func(t *testing.T) {
		dir := t.TempDir()
		db := openTempIDTable(t, filepath.Join(dir, "mysql.db"), "notes", seq(1, 15))
		require.NoError(t, db.Exec("CREATE TABLE v2_detections (id INTEGER PRIMARY KEY)").Error)
		for _, id := range seq(1, 15) {
			require.NoError(t, db.Exec("INSERT INTO v2_detections (id) VALUES (?)", id).Error)
		}
		require.NoError(t, db.Exec("CREATE TABLE migration_dirty_ids (detection_id INTEGER PRIMARY KEY, created_at INTEGER)").Error)

		assert.True(t, legacyTailReconciledInV2(db, db, 10, "notes", "v2_detections", "migration_dirty_ids", testStartupLogger()))
	})

	t.Run("MySQL single-handle shape: missing tail record defers", func(t *testing.T) {
		dir := t.TempDir()
		db := openTempIDTable(t, filepath.Join(dir, "mysql.db"), "notes", seq(1, 15))
		require.NoError(t, db.Exec("CREATE TABLE v2_detections (id INTEGER PRIMARY KEY)").Error)
		for _, id := range seq(1, 13) { // ids 14 and 15 never reached v2
			require.NoError(t, db.Exec("INSERT INTO v2_detections (id) VALUES (?)", id).Error)
		}
		require.NoError(t, db.Exec("CREATE TABLE migration_dirty_ids (detection_id INTEGER PRIMARY KEY, created_at INTEGER)").Error)

		assert.False(t, legacyTailReconciledInV2(db, db, 10, "notes", "v2_detections", "migration_dirty_ids", testStartupLogger()))
	})
}

// sqliteSettings builds SQLite-enabled settings pointing at the given legacy path.
func sqliteSettings(path string) *conf.Settings {
	s := &conf.Settings{}
	s.Output.SQLite.Enabled = true
	s.Output.SQLite.Path = path
	return s
}

// TestHasUnmigratedLegacyRecords_DualWriteResidueAllInV2 is the issue #3991 regression:
// a completed migration that is still dual-writing keeps legacy notes growing past the
// watermark, but those records are also written to v2. They must NOT be treated as
// unmigrated, so promotion can proceed on this boot.
func TestHasUnmigratedLegacyRecords_DualWriteResidueAllInV2(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// Legacy has 13 records; migration watermark is 10, so ids 11-13 are dual-write
	// residue. All three are present in v2.
	createLegacySQLite(t, legacyPath, 13)
	createCompletedV2MigrationDB(t, tmpDir, 10)
	insertV2DetectionIDs(t, V2MigrationPathFromConfigured(legacyPath), []uint{11, 12, 13})

	hasUnmigrated := HasUnmigratedLegacyRecords(sqliteSettings(legacyPath), testStartupLogger())
	assert.False(t, hasUnmigrated,
		"dual-write residue already present in v2 must not block promotion (#3991)")
}

// TestHasUnmigratedLegacyRecords_PartialStragglers verifies that genuinely missing tail
// records (present in legacy, absent from v2) still defer promotion.
func TestHasUnmigratedLegacyRecords_PartialStragglers(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// Legacy has 15 records past-watermark 11-15; only 11-13 made it into v2.
	createLegacySQLite(t, legacyPath, 15)
	createCompletedV2MigrationDB(t, tmpDir, 10)
	insertV2DetectionIDs(t, V2MigrationPathFromConfigured(legacyPath), []uint{11, 12, 13})

	hasUnmigrated := HasUnmigratedLegacyRecords(sqliteSettings(legacyPath), testStartupLogger())
	assert.True(t, hasUnmigrated,
		"records present in legacy but absent from v2 must defer promotion")
}

// TestHasUnmigratedLegacyRecords_DirtyIDDefers verifies that an outstanding dirty ID
// defers promotion even when every legacy tail record is present in v2. Tail sync can
// advance the watermark past a record it failed to migrate, so the dirty-ID set is the
// guard that catches it.
func TestHasUnmigratedLegacyRecords_DirtyIDDefers(t *testing.T) {
	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	createLegacySQLite(t, legacyPath, 13)
	createCompletedV2MigrationDB(t, tmpDir, 10)
	sidecar := V2MigrationPathFromConfigured(legacyPath)
	insertV2DetectionIDs(t, sidecar, []uint{11, 12, 13})
	insertV2DirtyIDs(t, sidecar, []uint{7}) // an unreconciled dual-write below the watermark

	hasUnmigrated := HasUnmigratedLegacyRecords(sqliteSettings(legacyPath), testStartupLogger())
	assert.True(t, hasUnmigrated,
		"an outstanding dirty ID must defer promotion")
}

// TestMoveSQLiteDBFiles_PreservesSidecars proves the consolidation rename carries the
// -wal and -shm sidecars alongside the main database instead of discarding them, so no
// committed WAL data is lost during promotion (issue #3991).
func TestMoveSQLiteDBFiles_PreservesSidecars(t *testing.T) {
	dir := t.TempDir()
	from := filepath.Join(dir, "src.db")
	to := filepath.Join(dir, "dst.db")

	require.NoError(t, os.WriteFile(from, []byte("main-db"), 0o600))
	require.NoError(t, os.WriteFile(from+"-wal", []byte("wal-frames"), 0o600))
	require.NoError(t, os.WriteFile(from+"-shm", []byte("shm-index"), 0o600))

	require.NoError(t, moveSQLiteDBFiles(from, to, testStartupLogger()))

	// Source files are gone.
	assert.NoFileExists(t, from)
	assert.NoFileExists(t, from+"-wal")
	assert.NoFileExists(t, from+"-shm")

	// Destination carries the main file and both sidecars with their content intact.
	assert.FileExists(t, to)
	walBytes, err := os.ReadFile(to + "-wal") //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "wal-frames", string(walBytes), "WAL sidecar content must survive the move")
	shmBytes, err := os.ReadFile(to + "-shm") //nolint:gosec // test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "shm-index", string(shmBytes))
}

// TestCheckpointSQLiteWAL_FoldsWALIntoMainFile proves that checkpointing folds
// uncheckpointed WAL frames into the main database file (so a later rename of the main
// file alone carries the data) and truncates the WAL to zero bytes.
func TestCheckpointSQLiteWAL_FoldsWALIntoMainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.db")

	// Writer connection kept open (idle) so closing it does not trigger the implicit
	// last-connection checkpoint; its committed frames stay in the on-disk -wal file.
	writer, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	writerSQL, err := writer.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = writerSQL.Close() })

	require.NoError(t, writer.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, writer.Exec("PRAGMA wal_autocheckpoint=0").Error)
	require.NoError(t, writer.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY)").Error)
	require.NoError(t, writer.Exec("INSERT INTO t (id) VALUES (1), (2), (3)").Error)

	walInfo, err := os.Stat(path + "-wal")
	require.NoError(t, err)
	require.Positive(t, walInfo.Size(), "precondition: WAL should hold uncheckpointed frames")

	require.NoError(t, checkpointSQLiteWAL(path, testStartupLogger()))

	walInfo, err = os.Stat(path + "-wal")
	require.NoError(t, err)
	assert.Zero(t, walInfo.Size(), "TRUNCATE checkpoint must empty the WAL file")

	// A fresh reader sees the data from the main file.
	reader, err := gorm.Open(sqlite.Open(readOnlyDSN(path)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	readerSQL, err := reader.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = readerSQL.Close() })
	var count int64
	require.NoError(t, reader.Table("t").Count(&count).Error)
	assert.Equal(t, int64(3), count, "checkpointed rows must be readable from the main file")
}

// TestCheckAndConsolidateAtStartup_PreservesUncheckpointedV2WAL proves the end-to-end
// promotion path does not discard committed data that lives only in the v2 sidecar's WAL
// at consolidation time, i.e. the unclean-shutdown case that this promotion fix now
// exposes on busy installs (issue #3991).
func TestCheckAndConsolidateAtStartup_PreservesUncheckpointedV2WAL(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := V2MigrationPathFromConfigured(configuredPath)

	// Legacy database with rows (it will be renamed to a backup) and a completed v2
	// sidecar (checkpointed and closed by the helper).
	createLegacySQLite(t, configuredPath, 5)
	createCompletedV2MigrationDB(t, tmpDir, 5)

	// Stage detection rows 6-8 as an uncheckpointed WAL image on the sidecar, mimicking a
	// power loss after commit but before checkpoint.
	stageUncheckpointedV2WAL(t, v2Path, []uint{6, 7, 8})

	consolidated, err := CheckAndConsolidateAtStartup(configuredPath, testStartupLogger())
	require.NoError(t, err)
	require.True(t, consolidated, "a completed v2 sidecar next to a legacy DB must consolidate")

	// The promoted database at the configured path must contain the WAL-resident rows.
	promoted, err := gorm.Open(sqlite.Open(readOnlyDSN(configuredPath)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	promotedSQL, err := promoted.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = promotedSQL.Close() })

	var count int64
	require.NoError(t, promoted.Table("detections").Where("id IN ?", []uint{6, 7, 8}).Count(&count).Error)
	assert.Equal(t, int64(3), count, "uncheckpointed v2 WAL rows must survive consolidation")
}

// TestResumeConsolidation_PreservesUncheckpointedV2WAL proves the interrupted-consolidation
// resume path (Step 1 of CheckAndConsolidateAtStartup) also preserves committed data that
// lives only in the v2 sidecar's WAL, rather than discarding it as the previous blind
// cleanup did (issue #3991).
func TestResumeConsolidation_PreservesUncheckpointedV2WAL(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")
	v2Path := V2MigrationPathFromConfigured(configuredPath)
	backupPath := filepath.Join(tmpDir, "birdnet.db.backup")

	// Completed v2 sidecar with rows 6-8 staged only in its uncheckpointed WAL.
	createCompletedV2MigrationDB(t, tmpDir, 5)
	stageUncheckpointedV2WAL(t, v2Path, []uint{6, 7, 8})

	// Interrupted consolidation at step 8: the legacy DB was already renamed to the
	// backup, the v2 sidecar was not yet renamed to the configured path, and the
	// configured path does not exist.
	require.NoError(t, os.WriteFile(backupPath, []byte("legacy-backup"), 0o600))
	require.NoError(t, WriteConsolidationState(tmpDir, &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2Path,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
	}))

	resumed, newPath, err := ResumeConsolidation(tmpDir, testStartupLogger())
	require.NoError(t, err)
	require.True(t, resumed, "an interrupted step-8 consolidation must resume")
	assert.Equal(t, configuredPath, newPath)

	promoted, err := gorm.Open(sqlite.Open(readOnlyDSN(configuredPath)), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	promotedSQL, err := promoted.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = promotedSQL.Close() })

	var count int64
	require.NoError(t, promoted.Table("detections").Where("id IN ?", []uint{6, 7, 8}).Count(&count).Error)
	assert.Equal(t, int64(3), count, "uncheckpointed v2 WAL rows must survive resumed consolidation")
}

// stageUncheckpointedV2WAL writes detection rows into the sidecar at path and leaves them
// in an uncheckpointed on-disk WAL with no open connection, reproducing a crash image.
// It snapshots the (main db, -wal) pair before the writer's clean close checkpoints them,
// then restores that snapshot; the missing -shm is rebuilt by SQLite on the next open.
func stageUncheckpointedV2WAL(t *testing.T, path string, ids []uint) {
	t.Helper()

	writer, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, writer.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, writer.Exec("PRAGMA wal_autocheckpoint=0").Error)
	for _, id := range ids {
		require.NoError(t, writer.Exec(
			"INSERT INTO detections (id, model_id, label_id, detected_at, confidence) VALUES (?, 1, 1, ?, 0.5)",
			id, int64(id)).Error)
	}

	// Snapshot the pre-checkpoint on-disk state (main file plus populated WAL).
	holdDir := t.TempDir()
	heldDB := filepath.Join(holdDir, "db")
	heldWAL := filepath.Join(holdDir, "wal")
	copyFile(t, path, heldDB)
	copyFile(t, path+"-wal", heldWAL)

	// Clean close checkpoints and truncates the original WAL, but the snapshot above
	// preserved the uncheckpointed image.
	writerSQL, err := writer.DB()
	require.NoError(t, err)
	require.NoError(t, writerSQL.Close())

	// Restore the crash image: main file plus its uncheckpointed WAL, no -shm.
	_ = os.Remove(path + "-shm")
	copyFile(t, heldDB, path)
	copyFile(t, heldWAL, path+"-wal")
}
