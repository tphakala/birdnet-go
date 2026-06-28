//go:build linux

package staging

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqliteHeader is the 16-byte SQLite file header.
var sqliteHeader = append([]byte("SQLite format 3"), 0x00)

// writeMinimalSQLite creates a real, valid BirdNET-Pi-shaped SQLite db at path.
// Mirrors the schema from internal/imports/birdnetpi/source_test.go.
func writeMinimalSQLite(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE detections (
		Date TEXT, Time TEXT, Sci_Name TEXT, Com_Name TEXT, Confidence REAL,
		Lat REAL, Lon REAL, Cutoff REAL, Sens REAL, File_Name TEXT)`)
	require.NoError(t, err)
}

func TestStageCopiesAndValidates(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "birds.db")
	writeMinimalSQLite(t, src)

	dst := t.TempDir() // empty dir
	res, err := Stage(t.Context(), Options{
		Src: src, Dst: dst, UID: os.Getuid(), GID: os.Getgid(),
	})
	require.NoError(t, err)
	assert.FileExists(t, res.StagedDB)

	got, err := os.ReadFile(res.StagedDB)
	require.NoError(t, err)
	assert.Equal(t, sqliteHeader, got[:16])
}

func TestStageRejectsSymlinkSrc(t *testing.T) {
	dir := t.TempDir()
	realDB := filepath.Join(dir, "real.db")
	writeMinimalSQLite(t, realDB)
	link := filepath.Join(dir, "link.db")
	require.NoError(t, os.Symlink(realDB, link))

	_, err := Stage(t.Context(), Options{
		Src: link, Dst: t.TempDir(), UID: os.Getuid(), GID: os.Getgid(),
	})
	require.Error(t, err, "O_NOFOLLOW must reject a symlinked src")
}

func TestStageRejectsNonSQLite(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "birds.db")
	require.NoError(t, os.WriteFile(bad, []byte("not a database"), 0o600))

	_, err := Stage(t.Context(), Options{
		Src: bad, Dst: t.TempDir(), UID: os.Getuid(), GID: os.Getgid(),
	})
	require.ErrorIs(t, err, ErrNotSQLite)
}

func TestStageRejectsNonEmptyDst(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "birds.db")
	writeMinimalSQLite(t, src)

	dst := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dst, "x"), []byte("y"), 0o600))

	_, err := Stage(t.Context(), Options{
		Src: src, Dst: dst, UID: os.Getuid(), GID: os.Getgid(),
	})
	require.ErrorIs(t, err, ErrDstNotEmpty)
}

func TestStageRejectsNonSiblingAudio(t *testing.T) {
	dbDir := t.TempDir()
	src := filepath.Join(dbDir, "birds.db")
	writeMinimalSQLite(t, src)

	// Audio dir lives somewhere else entirely (the attack: --audio=/root/.ssh).
	elsewhere := t.TempDir()
	_, err := Stage(t.Context(), Options{
		Src: src, Audio: elsewhere, Dst: t.TempDir(),
		UID: os.Getuid(), GID: os.Getgid(),
	})
	require.ErrorIs(t, err, ErrInvalidOptions)
}

func TestStageRejectsSymlinkInAudioTree(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "birds.db")
	writeMinimalSQLite(t, src)

	audio := filepath.Join(base, "BirdSongs") // sibling of src, passes step 1
	require.NoError(t, os.Mkdir(audio, 0o700))
	secret := filepath.Join(base, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))
	// A leaf inside the audio tree is a symlink to the secret.
	require.NoError(t, os.Symlink(secret, filepath.Join(audio, "evil.wav")))

	dst := t.TempDir()
	_, err := Stage(t.Context(), Options{
		Src: src, Audio: audio, Dst: dst,
		UID: os.Getuid(), GID: os.Getgid(),
	})
	require.Error(t, err, "a symlink leaf in the audio tree must be rejected")
	// The secret must not have been staged.
	assert.NoFileExists(t, filepath.Join(dst, "BirdSongs", "evil.wav"))
}
