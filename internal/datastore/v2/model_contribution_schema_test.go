package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const (
	contribTable         = "detection_model_contributions"
	contribUniqueIndex   = "idx_contrib_detection_model"
	contribOldIndex      = "idx_detection_model_contributions_detection_id"
	contribModelIndex    = "idx_detection_model_contributions_model_id"
	contribDetectionsSQL = "INSERT INTO detections (id, model_id, label_id, detected_at, confidence, clip_name) VALUES (1, 1, 1, 1000, 0.9, 'old.wav')"
)

// contribStatColumns are the nullable per-model hit statistics columns.
var contribStatColumns = []string{"first_hit_at", "last_hit_at", "min_confidence", "confidence_sum"}

// sqliteIndexNames lists the index names on a SQLite table.
func sqliteIndexNames(t *testing.T, db *gorm.DB, table string) []string {
	t.Helper()
	var names []string
	require.NoError(t, db.Raw("SELECT name FROM pragma_index_list(?)", table).Scan(&names).Error)
	return names
}

// TestInitialize_SwapsContributionDetectionIndex verifies that a database created
// before the hit statistics columns existed gains them, trades the plain
// detection_id index for a model_id index, keeps the unique (detection_id,
// model_id) index and its rows, and that a further start changes nothing.
func TestInitialize_SwapsContributionDetectionIndex(t *testing.T) {
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())
	db := mgr.DB()

	cols, err := sqliteColumnLister(db, contribTable)
	require.NoError(t, err)
	for _, c := range contribStatColumns {
		assert.Contains(t, cols, c)
	}
	idx := sqliteIndexNames(t, db, contribTable)
	assert.Contains(t, idx, contribModelIndex)
	assert.Contains(t, idx, contribUniqueIndex)
	assert.NotContains(t, idx, contribOldIndex, "a fresh install never creates the plain detection_id index")

	// Recreate the pre-upgrade state with one old row.
	require.NoError(t, db.Exec("PRAGMA foreign_keys=OFF").Error)
	require.NoError(t, db.Exec("CREATE INDEX "+contribOldIndex+" ON "+contribTable+" (detection_id)").Error)
	require.NoError(t, db.Exec("DROP INDEX "+contribModelIndex).Error)
	for _, c := range contribStatColumns {
		require.NoError(t, db.Exec("ALTER TABLE "+contribTable+" DROP COLUMN "+c).Error)
	}
	require.NoError(t, db.Exec(contribDetectionsSQL).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO "+contribTable+" (detection_id, model_id, hit_count, max_confidence) VALUES (1, 1, 3, 0.9)").Error)
	require.NoError(t, db.Exec("PRAGMA foreign_keys=ON").Error)
	idx = sqliteIndexNames(t, db, contribTable)
	require.Contains(t, idx, contribOldIndex)
	require.NotContains(t, idx, contribModelIndex)

	assertUpgraded := func() {
		t.Helper()
		idx := sqliteIndexNames(t, db, contribTable)
		assert.NotContains(t, idx, contribOldIndex, "the redundant detection_id index must be dropped")
		assert.Contains(t, idx, contribModelIndex)
		assert.Contains(t, idx, contribUniqueIndex, "the unique index must survive the drop")
		cols, err := sqliteColumnLister(db, contribTable)
		require.NoError(t, err)
		for _, c := range contribStatColumns {
			assert.Contains(t, cols, c)
		}
	}

	require.NoError(t, mgr.Initialize())
	assertUpgraded()

	var row entities.DetectionModelContribution
	require.NoError(t, db.First(&row).Error)
	assert.Equal(t, 3, row.HitCount)
	assert.Nil(t, row.FirstHitAt)
	assert.Nil(t, row.LastHitAt)
	assert.Nil(t, row.MinConfidence)
	assert.Nil(t, row.ConfidenceSum)

	require.NoError(t, mgr.Initialize(), "a further start must be idempotent")
	assertUpgraded()
}
