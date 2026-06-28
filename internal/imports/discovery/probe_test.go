package discovery

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeBirdsDB(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "birds.db")
	db, err := sql.Open("sqlite3", p)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE detections (
		Date TEXT, Time TEXT, Sci_Name TEXT, Com_Name TEXT, Confidence REAL,
		Lat REAL, Lon REAL, Cutoff REAL, Sens REAL, File_Name TEXT)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO detections VALUES
		('2026-06-20','07:00:00','Parus major','Great Tit',0.8,0,0,0,1.0,'b.mp3')`)
	require.NoError(t, err)
	return p
}

func TestProbeCandidate_ValidDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeBirdsDB(t, dir)
	// sibling audio tree
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "BirdSongs", "Extracted"), 0o755))

	got := probeCandidate(t.Context(), p, KindLocal)
	assert.True(t, got.Valid)
	assert.Empty(t, got.Reason)
	assert.Equal(t, 1, got.DetectionCount)
	assert.Equal(t, "2026-06-20", got.LatestDate)
	assert.Equal(t, filepath.Join(dir, "BirdSongs"), got.AudioDirGuess)
	assert.Positive(t, got.Size)
}

func TestProbeCandidate_NotASqliteDatabase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "birds.db")
	require.NoError(t, os.WriteFile(p, []byte("not a database"), 0o600))

	got := probeCandidate(t.Context(), p, KindLocal)
	assert.False(t, got.Valid)
	assert.Contains(t, []string{ReasonInvalidSchema, ReasonOpenFailed}, got.Reason)
}
