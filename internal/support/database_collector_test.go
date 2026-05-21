package support

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

const testTableDetections = "detections"

// openTestDB creates a file-based SQLite database in a temp directory.
// File-based is required for PRAGMA tests and file-size collection.
func openTestDB(t *testing.T) (db *gorm.DB, dbPath string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath+"?_foreign_keys=ON"), &gorm.Config{
		Logger: gorm_logger.Discard,
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	return db, dbPath
}

// seedTestSchema creates tables that mirror a subset of the v2 schema.
func seedTestSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&entities.MigrationState{},
		&entities.AppMetadata{},
	))

	// Create a labels table with a detection referencing it (for FK testing)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS labels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS detections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		label_id INTEGER NOT NULL REFERENCES labels(id),
		confidence REAL NOT NULL DEFAULT 0.0
	)`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX IF NOT EXISTS idx_detections_label_id ON detections(label_id)`).Error)

	// Insert some test data
	require.NoError(t, db.Exec("INSERT INTO labels (id, name) VALUES (1, 'Turdus merula')").Error)
	require.NoError(t, db.Exec("INSERT INTO labels (id, name) VALUES (2, 'Parus major')").Error)
	require.NoError(t, db.Exec("INSERT INTO detections (label_id, confidence) VALUES (1, 0.95)").Error)
	require.NoError(t, db.Exec("INSERT INTO detections (label_id, confidence) VALUES (2, 0.87)").Error)
}

func TestCollectDatabaseInfo_SQLite(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, datastore.DialectSQLite, info.Dialect)
	assert.NotEmpty(t, info.EngineVersion)
	assert.Equal(t, "v2", info.SchemaVersion)
	assert.Positive(t, info.DatabaseSizeBytes)

	// PRAGMAs
	assert.NotEmpty(t, info.JournalMode)
	assert.NotEmpty(t, info.AutoVacuum)
	assert.Positive(t, info.PageSize)
	assert.Positive(t, info.PageCount)

	// Tables should include at least labels, detections, migration_states, app_metadata
	assert.GreaterOrEqual(t, len(info.Tables), 4)

	// Find the detections table and verify its schema
	var detectionsTable *TableSchema
	for i := range info.Tables {
		if info.Tables[i].Name == testTableDetections {
			detectionsTable = &info.Tables[i]
			break
		}
	}
	require.NotNil(t, detectionsTable, "detections table should be present")
	assert.Equal(t, int64(2), detectionsTable.RowCount)
	assert.GreaterOrEqual(t, len(detectionsTable.Columns), 3) // id, label_id, confidence

	// Verify column details
	var labelIDCol *ColumnInfo
	for i := range detectionsTable.Columns {
		if detectionsTable.Columns[i].Name == "label_id" {
			labelIDCol = &detectionsTable.Columns[i]
			break
		}
	}
	require.NotNil(t, labelIDCol)
	assert.False(t, labelIDCol.Nullable)
	assert.False(t, labelIDCol.PrimaryKey)

	// Verify index
	assert.NotEmpty(t, detectionsTable.Indexes)
	var labelIdx *IndexInfo
	for i := range detectionsTable.Indexes {
		if detectionsTable.Indexes[i].Name == "idx_detections_label_id" {
			labelIdx = &detectionsTable.Indexes[i]
			break
		}
	}
	require.NotNil(t, labelIdx)
	assert.Equal(t, []string{"label_id"}, labelIdx.Columns)
	assert.False(t, labelIdx.Unique)

	// Integrity check
	assert.Equal(t, "ok", info.IntegrityCheck)

	// No FK violations with valid data
	assert.Empty(t, info.FKViolations)
}

func TestCollectDatabaseInfo_FKViolations(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	// Disable FK enforcement, insert orphan, re-enable
	require.NoError(t, db.Exec("PRAGMA foreign_keys = OFF").Error)
	require.NoError(t, db.Exec("INSERT INTO detections (id, label_id, confidence) VALUES (100, 99999, 0.5)").Error)
	require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotEmpty(t, info.FKViolations)

	// Should find a violation in the detections table
	found := false
	for _, v := range info.FKViolations {
		if v.Table == "detections" && v.RowID == 100 {
			found = true
			assert.Equal(t, "labels", v.RefTable)
			break
		}
	}
	assert.True(t, found, "expected FK violation for orphaned detection row 100")
}

func TestCollectDatabaseInfo_SchemaContamination(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	// Add an extra column that shouldn't exist (schema contamination)
	require.NoError(t, db.Exec("ALTER TABLE detections ADD COLUMN spurious_col TEXT").Error)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)

	var detectionsTable *TableSchema
	for i := range info.Tables {
		if info.Tables[i].Name == testTableDetections {
			detectionsTable = &info.Tables[i]
			break
		}
	}
	require.NotNil(t, detectionsTable)

	// The spurious column should appear in the schema snapshot
	var foundSpurious bool
	for _, col := range detectionsTable.Columns {
		if col.Name == "spurious_col" {
			foundSpurious = true
			break
		}
	}
	assert.True(t, foundSpurious, "schema contamination (spurious_col) should be visible")
}

func TestCollectDatabaseInfo_EmptyDB(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, datastore.DialectSQLite, info.Dialect)
	assert.NotEmpty(t, info.EngineVersion)
	assert.Equal(t, "ok", info.IntegrityCheck)
	assert.Empty(t, info.FKViolations)
	assert.Nil(t, info.MigrationState)
	assert.Nil(t, info.AppMetadata)
}

func TestCollectDatabaseInfo_MigrationState(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	// Populate migration state
	now := time.Now().UTC()
	state := entities.MigrationState{
		ID:              1,
		State:           entities.MigrationStatusCompleted,
		CurrentPhase:    entities.MigrationPhaseNone,
		StartedAt:       &now,
		CompletedAt:     &now,
		TotalRecords:    1000,
		MigratedRecords: 1000,
	}
	require.NoError(t, db.Save(&state).Error)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	require.NotNil(t, info.MigrationState)

	assert.Equal(t, "completed", info.MigrationState.State)
	assert.Equal(t, int64(1000), info.MigrationState.TotalRecords)
	assert.Equal(t, int64(1000), info.MigrationState.MigratedRecords)
	assert.NotNil(t, info.MigrationState.StartedAt)
	assert.NotNil(t, info.MigrationState.CompletedAt)
}

func TestCollectDatabaseInfo_AppMetadata(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	require.NoError(t, db.Create(&entities.AppMetadata{Key: "wizard_completed", Value: "true"}).Error)
	require.NoError(t, db.Create(&entities.AppMetadata{Key: "last_version", Value: "0.8.0"}).Error)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	require.NotNil(t, info.AppMetadata)
	assert.Equal(t, "true", info.AppMetadata["wizard_completed"])
	assert.Equal(t, "0.8.0", info.AppMetadata["last_version"])
}

func TestCollectDatabaseInfo_PathScrubbing(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(t.Context())

	require.NoError(t, err)
	// The raw dbPath should not appear verbatim in the output
	assert.NotEqual(t, dbPath, info.DatabasePath)
}

func TestArchiveIncludesDatabaseInfo(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	// Create a collector with database info provider
	collector := NewCollector(t.TempDir(), t.TempDir(), "test-system", "0.1.0")
	dbCollector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	collector.SetDatabaseInfoProvider(dbCollector)

	opts := CollectorOptions{
		IncludeDatabaseInfo: true,
		IncludeSystemInfo:   true,
	}

	dump, err := collector.Collect(t.Context(), opts)
	require.NoError(t, err)
	require.NotNil(t, dump.DatabaseInfo)

	archive, err := collector.CreateArchive(t.Context(), dump, opts)
	require.NoError(t, err)

	// Read the ZIP and verify database_info.json is present
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	require.NoError(t, err)

	var foundDBInfo bool
	for _, f := range reader.File {
		if f.Name != "database_info.json" {
			continue
		}
		foundDBInfo = true

		rc, err := f.Open()
		require.NoError(t, err)
		t.Cleanup(func() { _ = rc.Close() })

		var dbInfo DatabaseInfo
		require.NoError(t, json.NewDecoder(rc).Decode(&dbInfo))

		assert.Equal(t, datastore.DialectSQLite, dbInfo.Dialect)
		assert.NotEmpty(t, dbInfo.Tables)
		break
	}
	assert.True(t, foundDBInfo, "database_info.json should be in the archive")
}

func TestCollectDatabaseInfo_ContextCancellation(t *testing.T) {
	t.Parallel()
	db, dbPath := openTestDB(t)
	seedTestSchema(t, db)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	collector := NewGormDatabaseInfoCollector(db, datastore.DialectSQLite, dbPath, "v2", "")
	info, err := collector.CollectDatabaseInfo(ctx)

	// Should still return a result (with errors noted), not fail outright
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, datastore.DialectSQLite, info.Dialect)
}
