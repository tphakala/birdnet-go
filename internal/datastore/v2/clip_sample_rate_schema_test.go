package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestInitialize_AddsClipSampleRateColumn verifies that AutoMigrate adds the
// nullable clip_sample_rate column to a detections table created without it,
// and that old rows read NULL.
func TestInitialize_AddsClipSampleRateColumn(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	db := mgr.DB()
	cols, err := sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	require.Contains(t, cols, "clip_sample_rate")

	// Simulate a database created before the column existed, with one old row.
	require.NoError(t, db.Exec("PRAGMA foreign_keys=OFF").Error)
	require.NoError(t, db.Exec(
		"INSERT INTO detections (model_id, label_id, detected_at, confidence, clip_name) VALUES (1, 1, 1000, 0.9, 'old.wav')").Error)
	require.NoError(t, db.Exec("ALTER TABLE detections DROP COLUMN clip_sample_rate").Error)
	cols, err = sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	require.NotContains(t, cols, "clip_sample_rate")
	require.NoError(t, db.Exec("PRAGMA foreign_keys=ON").Error)

	require.NoError(t, mgr.Initialize())

	cols, err = sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	assert.Contains(t, cols, "clip_sample_rate", "AutoMigrate must add the clip_sample_rate column")

	var det entities.Detection
	require.NoError(t, db.First(&det).Error)
	assert.Nil(t, det.ClipSampleRate, "pre-existing rows read NULL")
}
