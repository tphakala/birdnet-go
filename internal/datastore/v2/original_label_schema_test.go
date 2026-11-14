package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestInitialize_AddsOriginalLabelColumn verifies that AutoMigrate adds the
// nullable original_label_id column to a detections table created without it,
// that old rows read NULL and survive, and that it adds the column alone.
func TestInitialize_AddsOriginalLabelColumn(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewSQLiteManager(Config{DataDir: tmpDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	db := mgr.DB()
	colsWith, err := sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	require.Contains(t, colsWith, "original_label_id")

	// The column is a plain nullable column: an index or a foreign key on it
	// would make the migration heavier and, on SQLite, a table rebuild.
	var indexed int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM pragma_index_list('detections') il "+
			"JOIN pragma_index_info(il.name) ii WHERE ii.name = 'original_label_id'").Scan(&indexed).Error)
	require.Zero(t, indexed, "original_label_id has no index")
	var foreignKeys int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM pragma_foreign_key_list('detections') WHERE \"from\" = 'original_label_id'").
		Scan(&foreignKeys).Error)
	require.Zero(t, foreignKeys, "original_label_id has no foreign key")

	// Simulate a database created before the column existed, with one old row.
	require.NoError(t, db.Exec("PRAGMA foreign_keys=OFF").Error)
	require.NoError(t, db.Exec(
		"INSERT INTO detections (model_id, label_id, detected_at, confidence, clip_name) VALUES (1, 1, 1000, 0.9, 'old.wav')").Error)
	require.NoError(t, db.Exec("ALTER TABLE detections DROP COLUMN original_label_id").Error)
	colsWithout, err := sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	require.NotContains(t, colsWithout, "original_label_id")
	require.NoError(t, db.Exec("PRAGMA foreign_keys=ON").Error)

	require.NoError(t, mgr.Initialize())

	colsAfter, err := sqliteColumnLister(db, "detections")
	require.NoError(t, err)
	assert.ElementsMatch(t, colsWith, colsAfter, "AutoMigrate adds exactly the original_label_id column")

	var nullable int64
	require.NoError(t, db.Raw(
		"SELECT COUNT(*) FROM pragma_table_info('detections') WHERE name = 'original_label_id' AND \"notnull\" = 0").
		Scan(&nullable).Error)
	assert.Equal(t, int64(1), nullable, "the column is nullable")

	var det entities.Detection
	require.NoError(t, db.First(&det).Error)
	assert.Nil(t, det.OriginalLabelID, "pre-existing rows read NULL")
	require.NotNil(t, det.ClipName)
	assert.Equal(t, "old.wav", *det.ClipName, "the existing row survives")
}
