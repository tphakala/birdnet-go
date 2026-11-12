package analysis

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestIsV2DatabaseSafeToDelete_NoiseExamples pins that learned noise examples count
// as user data: they outlive their detections, so a database whose only rows are
// noise examples must not be deleted by schema self healing.
func TestIsV2DatabaseSafeToDelete_NoiseExamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noise.db")
	mgr, err := v2.NewSQLiteManager(v2.Config{DirectPath: path})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())

	log := logger.Global().Module("analysis")
	require.True(t, isV2DatabaseSafeToDelete(path, log), "an empty database is disposable")

	require.NoError(t, mgr.DB().Create(&entities.NoiseExample{
		EmbeddingSpace: "test/space", Dim: 1, Score: 0.9, Vector: []byte{0, 0, 0, 0}, CreatedAt: 1,
	}).Error)
	require.NoError(t, mgr.CheckpointWAL())
	require.NoError(t, mgr.Close())

	assert.False(t, isV2DatabaseSafeToDelete(path, log), "a database holding only noise examples is not")
}
