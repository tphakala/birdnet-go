package v2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
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
