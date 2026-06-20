package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"gorm.io/gorm"
)

func TestNewSQLiteManager(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	assert.NotNil(t, mgr.DB())
	assert.Equal(t, filepath.Join(tmpDir, "birdnet_v2.db"), mgr.Path())
	assert.False(t, mgr.IsMySQL())
}

// TestNewSQLiteManager_AppliesPerformancePragmas verifies the cold-read tuning
// pragmas are actually applied to an opened file database: mmap_size (set via a
// post-open Exec, since mattn has no DSN param) and the larger cache_size (DSN).
func TestNewSQLiteManager_AppliesPerformancePragmas(t *testing.T) {
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	var mmap int64
	require.NoError(t, mgr.DB().Raw("PRAGMA mmap_size").Scan(&mmap).Error)
	assert.Equal(t, int64(sqliteMmapSizeBytes), mmap,
		"mmap_size pragma should be applied (confirms the driver build has mmap enabled)")

	var cacheSize int64
	require.NoError(t, mgr.DB().Raw("PRAGMA cache_size").Scan(&cacheSize).Error)
	assert.Equal(t, int64(-16000), cacheSize, "cache_size pragma should reflect the DSN value")
}

func TestSQLiteManager_Initialize(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify all tables exist
	tables := []any{
		&entities.Label{},
		&entities.AIModel{},
		&entities.LabelType{},
		&entities.TaxonomicClass{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionPrediction{},
		&entities.DetectionReview{},
		&entities.DetectionComment{},
		&entities.DetectionLock{},
		&entities.MigrationState{},
	}

	for _, table := range tables {
		assert.True(t, mgr.DB().Migrator().HasTable(table), "table should exist: %T", table)
	}
}

func TestSQLiteManager_Initialize_SeedsDefaultModel(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify default model was seeded using the detection package constants
	var model entities.AIModel
	err = mgr.DB().Where("name = ? AND version = ?", detection.DefaultModelName, detection.DefaultModelVersion).First(&model).Error
	require.NoError(t, err)
	assert.Equal(t, detection.DefaultModelName, model.Name)
	assert.Equal(t, detection.DefaultModelVersion, model.Version)
	assert.Equal(t, entities.ModelTypeBird, model.ModelType)
}

func TestSQLiteManager_Initialize_CreatesMigrationState(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify migration state singleton was created
	var state entities.MigrationState
	err = mgr.DB().First(&state).Error
	require.NoError(t, err)
	assert.Equal(t, uint(1), state.ID)
	assert.Equal(t, entities.MigrationStatusIdle, state.State)
}

func TestSQLiteManager_Initialize_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Initialize twice - should not error
	err = mgr.Initialize()
	require.NoError(t, err)

	err = mgr.Initialize()
	require.NoError(t, err)

	// Verify only one default model exists
	var count int64
	err = mgr.DB().Model(&entities.AIModel{}).Where("name = ?", detection.DefaultModelName).Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Verify only one migration state exists
	err = mgr.DB().Model(&entities.MigrationState{}).Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestSQLiteManager_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	// Before creating manager, database doesn't exist
	assert.False(t, ExistsFromPath(configuredPath))

	mgr, err := NewSQLiteManager(Config{ConfiguredPath: configuredPath})
	require.NoError(t, err)

	// After opening, database exists
	assert.True(t, mgr.Exists())

	require.NoError(t, mgr.Close())
	assert.True(t, ExistsFromPath(configuredPath))
}

func TestSQLiteManager_Delete(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)

	err = mgr.Initialize()
	require.NoError(t, err)

	dbPath := mgr.Path()
	assert.FileExists(t, dbPath)

	err = mgr.Delete()
	require.NoError(t, err)

	// Database file should be removed
	_, err = os.Stat(dbPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSQLiteManager_TablePrefix(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// SQLite uses separate file, no prefix needed
	assert.Empty(t, mgr.TablePrefix())
}

func TestGetDataDirFromLegacyPath(t *testing.T) {
	tests := []struct {
		name       string
		legacyPath string
		want       string
	}{
		{
			name:       "absolute path",
			legacyPath: "/data/birdnet.db",
			want:       "/data",
		},
		{
			name:       "relative path",
			legacyPath: "data/birdnet.db",
			want:       "data",
		},
		{
			name:       "current directory",
			legacyPath: "birdnet.db",
			want:       ".",
		},
		{
			name:       "in-memory database",
			legacyPath: ":memory:",
			want:       "",
		},
		{
			name:       "file memory URI",
			legacyPath: "file::memory:?cache=shared",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDataDirFromLegacyPath(tt.legacyPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSQLiteManager_ForeignKeyConstraints(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Try to create a detection with invalid foreign keys - should fail
	det := entities.Detection{
		ModelID:    999, // Non-existent
		LabelID:    999, // Non-existent
		DetectedAt: 1234567890,
		Confidence: 0.95,
	}

	err = mgr.DB().Create(&det).Error
	require.Error(t, err, "foreign key constraint should prevent invalid references")
}

func TestSQLiteManager_CascadeDelete(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Get the BirdNET model (seeded by Initialize)
	var model entities.AIModel
	err = mgr.DB().First(&model).Error
	require.NoError(t, err)

	// Get or create the species label type
	var labelType entities.LabelType
	err = mgr.DB().FirstOrCreate(&labelType, entities.LabelType{Name: "species"}).Error
	require.NoError(t, err)

	// Create a label
	label := entities.Label{
		ScientificName: "Turdus merula",
		ModelID:        model.ID,
		LabelTypeID:    labelType.ID,
	}
	err = mgr.DB().Create(&label).Error
	require.NoError(t, err)

	// Create a detection referencing the label
	det := entities.Detection{
		ModelID:    model.ID,
		LabelID:    label.ID,
		DetectedAt: 1234567890,
		Confidence: 0.95,
	}
	err = mgr.DB().Create(&det).Error
	require.NoError(t, err)

	// Create a review for the detection
	review := entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    "correct",
	}
	err = mgr.DB().Create(&review).Error
	require.NoError(t, err)

	// Delete the detection - review should cascade
	err = mgr.DB().Delete(&det).Error
	require.NoError(t, err)

	// Review should be deleted
	var count int64
	err = mgr.DB().Model(&entities.DetectionReview{}).Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestSQLiteManager_SourceDeleteSetsNull(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Get the BirdNET model (seeded by Initialize)
	var model entities.AIModel
	err = mgr.DB().First(&model).Error
	require.NoError(t, err)

	// Get or create the species label type
	var labelType entities.LabelType
	err = mgr.DB().FirstOrCreate(&labelType, entities.LabelType{Name: "species"}).Error
	require.NoError(t, err)

	// Create a label
	label := entities.Label{
		ScientificName: "Turdus merula",
		ModelID:        model.ID,
		LabelTypeID:    labelType.ID,
	}
	err = mgr.DB().Create(&label).Error
	require.NoError(t, err)

	// Create an audio source
	source := entities.AudioSource{
		SourceURI:  "test-source",
		NodeName:   "test-node",
		SourceType: entities.SourceTypeALSA,
	}
	err = mgr.DB().Create(&source).Error
	require.NoError(t, err)

	// Create a detection referencing the source
	det := entities.Detection{
		ModelID:    model.ID,
		LabelID:    label.ID,
		SourceID:   &source.ID,
		DetectedAt: 1234567890,
		Confidence: 0.95,
	}
	err = mgr.DB().Create(&det).Error
	require.NoError(t, err)

	// Verify source ID is set
	assert.NotNil(t, det.SourceID)

	// Delete the audio source - detection should survive with NULL source
	err = mgr.DB().Delete(&source).Error
	require.NoError(t, err)

	// Reload the detection
	var reloadedDet entities.Detection
	err = mgr.DB().First(&reloadedDet, det.ID).Error
	require.NoError(t, err)

	// Detection should still exist but SourceID should be NULL
	assert.Nil(t, reloadedDet.SourceID, "SourceID should be NULL after source deletion")
	assert.Equal(t, det.ID, reloadedDet.ID)
	assert.InDelta(t, det.Confidence, reloadedDet.Confidence, 0.0001)
}

func TestSQLiteManager_DirectPath(t *testing.T) {
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "mydb.db")

	manager, err := NewSQLiteManager(Config{
		DirectPath: customPath, // Use exact path
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Close() })

	err = manager.Initialize()
	require.NoError(t, err)

	// Verify database was created at exact path
	_, err = os.Stat(customPath)
	require.NoError(t, err, "database should exist at direct path")

	// Verify no _v2 database was created
	_, err = os.Stat(filepath.Join(tmpDir, "birdnet_v2.db"))
	assert.True(t, os.IsNotExist(err), "should NOT create _v2 database")

	// Verify the Path() method returns the direct path
	assert.Equal(t, customPath, manager.Path())
}

func TestSQLiteManager_DirectPath_DeepNested(t *testing.T) {
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "data", "birds", "detections.db")

	// Ensure parent directories exist (this is caller's responsibility)
	err := os.MkdirAll(filepath.Dir(customPath), 0o750)
	require.NoError(t, err)

	manager, err := NewSQLiteManager(Config{
		DirectPath: customPath,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Close() })

	err = manager.Initialize()
	require.NoError(t, err)

	// Verify database at custom path
	_, err = os.Stat(customPath)
	assert.NoError(t, err, "database should exist at deep nested path")
}

func TestSQLiteManager_DirectPath_TakesPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom.db")

	// Even if DataDir is set, DirectPath should take precedence
	manager, err := NewSQLiteManager(Config{
		DataDir:    tmpDir,
		DirectPath: customPath,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Close() })

	err = manager.Initialize()
	require.NoError(t, err)

	// Verify custom path was used, not DataDir/birdnet_v2.db
	_, err = os.Stat(customPath)
	require.NoError(t, err, "should use DirectPath")

	_, err = os.Stat(filepath.Join(tmpDir, "birdnet_v2.db"))
	assert.True(t, os.IsNotExist(err), "should NOT use DataDir")
}

// TestSQLiteManager_NoReverseForeignKey verifies that GORM doesn't create
// spurious foreign keys due to field name collisions. This was a real bug where
// AudioSource.SourceID (a string business identifier) caused GORM to create
// a reverse FK from audio_sources.source_id -> detections.id.
// The fix was to rename AudioSource.SourceID to AudioSource.SourceURI.
func TestSQLiteManager_NoReverseForeignKey(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	err = mgr.Initialize()
	require.NoError(t, err)

	// Check that audio_sources has no foreign keys pointing to detections
	rows, err := mgr.DB().Raw("PRAGMA foreign_key_list('audio_sources')").Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		err := rows.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match)
		require.NoError(t, err)
		// audio_sources should have NO foreign keys pointing to detections
		assert.NotEqual(t, "detections", table,
			"audio_sources should not have FK to detections (GORM field name collision bug)")
	}
	require.NoError(t, rows.Err(), "error during foreign key iteration")

	// Verify audio_sources.source_uri is a varchar, not an integer
	var tableSQL string
	row := mgr.DB().Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name='audio_sources'").Row()
	err = row.Scan(&tableSQL)
	require.NoError(t, err)

	assert.Contains(t, tableSQL, "source_uri` varchar(500)",
		"source_uri should be varchar(500), not integer")
	assert.NotContains(t, tableSQL, "source_id",
		"audio_sources should not have source_id column (renamed to source_uri)")
}

func TestCleanupLegacySchemaContamination_DropsEmptyTable(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Create the image_caches table with an orphaned scientific_name column
	// that would have been added by the legacy AutoMigrate bug.
	err = mgr.DB().Exec(`CREATE TABLE IF NOT EXISTS image_caches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		species_code TEXT,
		url TEXT,
		cached_at DATETIME,
		scientific_name TEXT NOT NULL DEFAULT ''
	)`).Error
	require.NoError(t, err)

	// Verify the orphaned column exists before cleanup
	var colCount int64
	err = mgr.DB().Raw("SELECT COUNT(*) FROM pragma_table_info('image_caches') WHERE name = 'scientific_name'").Scan(&colCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), colCount, "scientific_name column should exist before cleanup")

	// Run cleanup — should succeed with no error
	err = mgr.cleanupLegacySchemaContamination()
	require.NoError(t, err)

	// Verify the scientific_name column is gone
	err = mgr.DB().Raw("SELECT COUNT(*) FROM pragma_table_info('image_caches') WHERE name = 'scientific_name'").Scan(&colCount).Error
	// Table may have been dropped entirely (empty table fallback), so handle both cases
	if err != nil {
		// Table was dropped — that's fine for an empty table
		colCount = 0
	}
	assert.Equal(t, int64(0), colCount, "scientific_name column should not exist after cleanup")
}

func TestCleanupLegacySchemaContamination_PreservesPopulatedTable(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Create the image_caches table with an orphaned scientific_name column
	err = mgr.DB().Exec(`CREATE TABLE IF NOT EXISTS image_caches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		species_code TEXT,
		url TEXT,
		cached_at DATETIME,
		scientific_name TEXT NOT NULL DEFAULT ''
	)`).Error
	require.NoError(t, err)

	// Insert a row so the table is populated
	err = mgr.DB().Exec(`INSERT INTO image_caches (species_code, url, cached_at, scientific_name) VALUES ('turdmer', 'http://example.com/img.jpg', datetime('now'), 'Turdus merula')`).Error
	require.NoError(t, err)

	// Run cleanup — on modern SQLite (3.35.0+), DROP COLUMN should succeed
	err = mgr.cleanupLegacySchemaContamination()
	require.NoError(t, err)

	// Verify the scientific_name column is gone (modern SQLite can DROP COLUMN)
	var colCount int64
	err = mgr.DB().Raw("SELECT COUNT(*) FROM pragma_table_info('image_caches') WHERE name = 'scientific_name'").Scan(&colCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), colCount, "scientific_name column should be removed by DROP COLUMN on modern SQLite")

	// Verify the row is still there
	var rowCount int64
	err = mgr.DB().Raw("SELECT COUNT(*) FROM image_caches").Scan(&rowCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), rowCount, "existing data should be preserved after DROP COLUMN")
}

func TestScrubErrorWithPaths(t *testing.T) {
	t.Parallel()

	// Verify file paths are replaced with anonymized versions
	errMsg := "open /home/john/data/birdnet.db: permission denied"
	result := scrubErrorWithPaths(errMsg, "/home/john/data/birdnet.db")
	assert.NotContains(t, result, "/home/john")
	assert.NotContains(t, result, "birdnet.db")
}

func TestScrubErrorWithPaths_EmptyPath(t *testing.T) {
	t.Parallel()

	// Empty paths should be skipped safely
	errMsg := "some error"
	result := scrubErrorWithPaths(errMsg, "")
	assert.Equal(t, "some error", result)
}

func TestReportInitFailure_SentryNotInitialized(t *testing.T) {
	t.Parallel()
	// Should not panic when Sentry hub is not initialized
	assert.NotPanics(t, func() {
		reportInitFailure("sqlite", "AutoMigrate", fmt.Errorf("disk full"), "/tmp/test.db")
	})
}

func TestSQLiteManager_PeriodicCheckpoint(t *testing.T) {
	t.Parallel()

	t.Run("start and stop cleanly", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
		require.NoError(t, err)
		t.Cleanup(func() { _ = mgr.Close() })

		sqliteMgr := mgr

		// Start should not panic
		assert.NotPanics(t, func() {
			sqliteMgr.StartPeriodicCheckpoint()
		})

		// Stop should not panic or hang
		assert.NotPanics(t, func() {
			sqliteMgr.StopPeriodicCheckpoint()
		})
	})

	t.Run("stop is idempotent", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
		require.NoError(t, err)
		t.Cleanup(func() { _ = mgr.Close() })

		sqliteMgr := mgr
		sqliteMgr.StartPeriodicCheckpoint()
		sqliteMgr.StopPeriodicCheckpoint()

		// Second stop should not panic
		assert.NotPanics(t, func() {
			sqliteMgr.StopPeriodicCheckpoint()
		})
	})

	t.Run("manual checkpoint succeeds", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
		require.NoError(t, err)
		t.Cleanup(func() { _ = mgr.Close() })

		// Initialize schema to create tables and WAL entries
		require.NoError(t, mgr.Initialize())

		// Manual PASSIVE checkpoint should succeed
		err = mgr.DB().Exec("PRAGMA wal_checkpoint(PASSIVE)").Error
		assert.NoError(t, err, "PASSIVE WAL checkpoint should succeed")
	})
}

func TestValidateV2SchemaIntegrity_DetectsContamination(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Initialize normally to create clean schema
	err = mgr.Initialize()
	require.NoError(t, err)

	// Add an unexpected column to image_caches (table should be empty)
	err = mgr.DB().Exec("ALTER TABLE image_caches ADD COLUMN bogus_column TEXT DEFAULT ''").Error
	require.NoError(t, err)

	// Verify the table is empty (no image cache rows inserted by Initialize)
	var rowCount int64
	err = mgr.DB().Raw("SELECT COUNT(*) FROM image_caches").Scan(&rowCount).Error
	require.NoError(t, err)
	require.Equal(t, int64(0), rowCount, "image_caches should be empty after Initialize")

	// Run validation — should succeed by dropping the empty contaminated table
	err = mgr.validateV2SchemaIntegrity()
	require.NoError(t, err)

	// The table should have been dropped — verify via raw SQLite query
	// (GORM's HasTable may use cached schema information)
	var tableCount int64
	err = mgr.DB().Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='image_caches'").Scan(&tableCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(0), tableCount,
		"image_caches should have been dropped because it was empty and contaminated")
}

func TestValidateV2SchemaIntegrity_ToleratesPopulatedContamination(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Initialize normally to create clean schema
	err = mgr.Initialize()
	require.NoError(t, err)

	// Temporarily disable FK checks so we can insert a test row without valid references
	err = mgr.DB().Exec("PRAGMA foreign_keys = OFF").Error
	require.NoError(t, err)

	// Insert a row first (using the real schema columns), then add an unexpected column
	err = mgr.DB().Exec("INSERT INTO image_caches (provider_name, label_id, source_provider, url, cached_at) VALUES ('wikimedia', 1, 'wikimedia', 'http://example.com/img.jpg', datetime('now'))").Error
	require.NoError(t, err)

	// Re-enable FK checks
	err = mgr.DB().Exec("PRAGMA foreign_keys = ON").Error
	require.NoError(t, err)

	err = mgr.DB().Exec("ALTER TABLE image_caches ADD COLUMN bogus_column TEXT DEFAULT ''").Error
	require.NoError(t, err)

	// Run validation: extra columns in populated tables are tolerated (warn, not error).
	// This changed from the original behavior (ErrV2SchemaCorrupted) to prevent
	// the cascade failure from Discussion #3210.
	err = mgr.validateV2SchemaIntegrity()
	require.NoError(t, err, "extra columns in populated tables should be tolerated")
}

func TestValidateV2SchemaIntegrity_PassesCleanSchema(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	// Initialize normally to create clean schema
	err = mgr.Initialize()
	require.NoError(t, err)

	// Run validation on a clean schema - should pass with no error
	err = mgr.validateV2SchemaIntegrity()
	require.NoError(t, err)
}

// TestSchemaEvolution_ExtraColumnsDoNotBlockInitialize reproduces Discussion #3210:
// a database created on an earlier nightly has extra columns from schema evolution
// (e.g. label_count in ai_models, label_type/taxonomic_class in labels).
// These extra columns are harmless (GORM ignores them) and must not prevent
// Initialize from running AutoMigrate.
func TestSchemaEvolution_ExtraColumnsDoNotBlockInitialize(t *testing.T) {
	tmpDir := t.TempDir()

	// Step 1: Create a clean v2 database
	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	err = mgr.Initialize()
	require.NoError(t, err)

	// Step 2: Add extra columns that simulate schema evolution
	// (columns that existed in earlier entity versions but were later removed)
	extraColumns := []struct {
		table  string
		column string
		sql    string
	}{
		{"ai_models", "label_count", "ALTER TABLE ai_models ADD COLUMN label_count INTEGER DEFAULT 0"},
		{"labels", "label_type", "ALTER TABLE labels ADD COLUMN label_type TEXT DEFAULT 'species'"},
		{"labels", "taxonomic_class", "ALTER TABLE labels ADD COLUMN taxonomic_class TEXT DEFAULT ''"},
		{"detections", "sensitivity", "ALTER TABLE detections ADD COLUMN sensitivity REAL"},
		{"detections", "threshold", "ALTER TABLE detections ADD COLUMN threshold REAL"},
		{"detections", "created_at", "ALTER TABLE detections ADD COLUMN created_at DATETIME"},
		{"daily_events", "created_at", "ALTER TABLE daily_events ADD COLUMN created_at DATETIME"},
		{"daily_events", "updated_at", "ALTER TABLE daily_events ADD COLUMN updated_at DATETIME"},
	}
	for _, ec := range extraColumns {
		err = mgr.DB().Exec(ec.sql).Error
		require.NoError(t, err, "failed to add %s.%s", ec.table, ec.column)
	}

	// Step 3: Insert data into contaminated tables (mimics a real user's database)
	// ai_models already has a seeded row from Initialize, so just update it with the extra column
	err = mgr.DB().Exec("UPDATE ai_models SET label_count = 6522 WHERE name = 'BirdNET'").Error
	require.NoError(t, err)
	err = mgr.DB().Exec("INSERT INTO daily_events (date, sunrise, sunset, country, city_name) VALUES ('2026-05-20', 1716184800, 1716228000, 'US', 'Test City')").Error
	require.NoError(t, err)

	// Insert a label so we can insert a detection with valid FK
	err = mgr.DB().Exec("INSERT INTO labels (scientific_name, model_id, label_type_id) VALUES ('Turdus merula', 1, 1)").Error
	require.NoError(t, err)
	// Insert a detection row so the table is populated (mimics 463K detections in user's DB)
	err = mgr.DB().Exec("INSERT INTO detections (model_id, label_id, detected_at, confidence) VALUES (1, 1, 1716184800, 0.85)").Error
	require.NoError(t, err)

	// Close and reopen to simulate app restart after upgrade
	err = mgr.Close()
	require.NoError(t, err)

	// Step 4: Re-open and Initialize (simulating upgrade to newer build)
	mgr2, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr2.Close() })

	// This is the critical assertion: Initialize must succeed despite extra columns.
	// Before the fix, validateV2SchemaIntegrity() would return ErrV2SchemaCorrupted
	// and block AutoMigrate from running.
	err = mgr2.Initialize()
	require.NoError(t, err, "Initialize must succeed with harmless extra columns from schema evolution")

	// Verify that the extra columns still exist (they're harmless, not dropped)
	var colCount int64
	err = mgr2.DB().Raw("SELECT COUNT(*) FROM pragma_table_info('ai_models') WHERE name = 'label_count'").Scan(&colCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), colCount, "extra columns should be preserved (harmless)")

	// Verify GORM operations work despite extra columns
	var models []entities.AIModel
	err = mgr2.DB().Find(&models).Error
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(models), 1, "should be able to query with extra columns present")
}

// TestValidateV2SchemaIntegrity_DetectsMissingColumn covers GitHub #3211: GORM
// AutoMigrate sometimes silently fails to add a newly-introduced column to an existing
// table. The post-AutoMigrate validator must catch this so callers can surface a real
// error instead of letting INSERTs fail with "table has no column named X".
func TestValidateV2SchemaIntegrity_DetectsMissingColumn(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	require.NoError(t, mgr.Initialize())

	// Disable FK checks so we can populate detections without lookup setup.
	require.NoError(t, mgr.DB().Exec("PRAGMA foreign_keys = OFF").Error)
	require.NoError(t, mgr.DB().Exec(
		"INSERT INTO labels (scientific_name, model_id, label_type_id) VALUES ('Turdus merula', 1, 1)").Error)
	require.NoError(t, mgr.DB().Exec(
		"INSERT INTO detections (model_id, label_id, detected_at, confidence) VALUES (1, 1, 1716184800, 0.85)").Error)
	require.NoError(t, mgr.DB().Exec("PRAGMA foreign_keys = ON").Error)

	// Simulate AutoMigrate silently failing to add the `unlikely` column that was
	// introduced in PR #3072 / commit e805e6eb0.
	require.NoError(t, mgr.DB().Exec("ALTER TABLE detections DROP COLUMN unlikely").Error)

	err = mgr.validateV2SchemaIntegrity()
	require.Error(t, err, "missing column must be reported as corruption")
	require.ErrorIs(t, err, ErrV2SchemaCorrupted)
	assert.Contains(t, err.Error(), "detections", "error should name the table")
	assert.Contains(t, err.Error(), "unlikely", "error should name the missing column")
}

// TestValidateV2SchemaIntegrity_DetectsMissingColumnEmptyTable verifies missing column
// detection also fires on empty tables. Row count must not affect corruption verdict.
func TestValidateV2SchemaIntegrity_DetectsMissingColumnEmptyTable(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	require.NoError(t, mgr.Initialize())

	require.NoError(t, mgr.DB().Exec("ALTER TABLE detections DROP COLUMN unlikely").Error)

	err = mgr.validateV2SchemaIntegrity()
	require.Error(t, err, "missing column must be reported even on empty tables")
	require.ErrorIs(t, err, ErrV2SchemaCorrupted)
}

// TestValidateV2SchemaIntegrity_MissingColumnWinsOverExtras verifies that when a table
// has both extra and missing columns, the missing-column corruption is reported.
// Extra columns alone are harmless (additive-only rule from PR #3222), but a missing
// column means GORM-generated INSERTs will fail.
func TestValidateV2SchemaIntegrity_MissingColumnWinsOverExtras(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	require.NoError(t, mgr.Initialize())

	// Add a harmless extra column from older schema evolution.
	require.NoError(t, mgr.DB().Exec(
		"ALTER TABLE detections ADD COLUMN legacy_extra TEXT DEFAULT ''").Error)
	// Also drop a required column.
	require.NoError(t, mgr.DB().Exec("ALTER TABLE detections DROP COLUMN unlikely").Error)

	err = mgr.validateV2SchemaIntegrity()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrV2SchemaCorrupted)
	assert.Contains(t, err.Error(), "unlikely")
}

// TestValidateSchemaIntegrity_MySQLParity_DetectsMissingColumn exercises the shared
// validator with a stub columnLister that mimics MySQL information_schema returning a
// table missing a column. SQLite is used as the actual backing store so we can keep
// the test hermetic, but the validator path covered is the MySQL one.
func TestValidateSchemaIntegrity_MySQLParity_DetectsMissingColumn(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	require.NoError(t, mgr.Initialize())

	// Stub lister: returns the real SQLite columns for every table EXCEPT
	// `detections`, where it drops the `unlikely` column to simulate a MySQL
	// information_schema result on a database that pre-dates PR #3072.
	stubLister := func(db *gorm.DB, tableName string) ([]string, error) {
		cols, listErr := sqliteColumnLister(db, tableName)
		if listErr != nil {
			return nil, listErr
		}
		if tableName != "detections" {
			return cols, nil
		}
		filtered := make([]string, 0, len(cols))
		for _, c := range cols {
			if c == "unlikely" {
				continue
			}
			filtered = append(filtered, c)
		}
		return filtered, nil
	}

	err = validateSchemaIntegrity(mgr.DB(), mgr.log, "mysql", stubLister)
	require.Error(t, err, "MySQL listing path must also report missing columns")
	require.ErrorIs(t, err, ErrV2SchemaCorrupted)
	assert.Contains(t, err.Error(), "detections")
	assert.Contains(t, err.Error(), "unlikely")
}

// TestInitialize_PostAutoMigrate_HealsDroppedColumn verifies that validation runs
// AFTER AutoMigrate. If AutoMigrate successfully re-adds a column that was previously
// missing, Initialize must succeed and not surface a stale "missing column" error.
// This pins the ordering change so a future refactor that moves the validator back
// before AutoMigrate fails this test loudly.
func TestInitialize_PostAutoMigrate_HealsDroppedColumn(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())

	// Drop the column that PR #3072 added so the schema looks like a pre-PR #3072
	// database. AutoMigrate on the next Initialize should re-add it.
	require.NoError(t, mgr.DB().Exec("ALTER TABLE detections DROP COLUMN unlikely").Error)
	require.NoError(t, mgr.Close())

	mgr2, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr2.Close() })

	require.NoError(t, mgr2.Initialize(),
		"Initialize must succeed: AutoMigrate should re-add the missing column "+
			"and the post-AutoMigrate validator should then see a healthy schema")

	var colCount int64
	require.NoError(t, mgr2.DB().Raw(
		"SELECT COUNT(*) FROM pragma_table_info('detections') WHERE name = 'unlikely'",
	).Scan(&colCount).Error)
	assert.Equal(t, int64(1), colCount, "AutoMigrate must have re-added the column")
}

func TestSQLiteManager_Close_NilsOutDB(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)

	require.NotNil(t, mgr.DB(), "DB should be non-nil before Close")

	err = mgr.Close()
	require.NoError(t, err)

	assert.Nil(t, mgr.DB(), "DB should be nil after Close to prevent stale reference queries")
}
