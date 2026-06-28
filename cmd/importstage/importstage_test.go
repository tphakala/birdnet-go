//go:build linux

package importstage

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// itoa converts an int to a string, for building --flag=value args.
func itoa(n int) string { return strconv.Itoa(n) }

// writeMinimalSQLite creates a minimal BirdNET-Pi-shaped SQLite db at path.
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

func TestImportStageCommandStages(t *testing.T) {
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalSQLite(t, src)
	dst := t.TempDir()

	cmd := Command(&conf.Settings{})
	cmd.SetArgs([]string{
		"--src=" + src,
		"--dst=" + dst,
		"--uid=" + itoa(os.Getuid()),
		"--gid=" + itoa(os.Getgid()),
	})
	require.NoError(t, cmd.Execute())
	require.FileExists(t, filepath.Join(dst, "birds.db"))
}

func TestImportStageCommandRejectsNonEmptyDst(t *testing.T) {
	src := filepath.Join(t.TempDir(), "birds.db")
	writeMinimalSQLite(t, src)
	dst := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dst, "existing"), []byte("x"), 0o600))

	cmd := Command(&conf.Settings{})
	cmd.SetArgs([]string{
		"--src=" + src,
		"--dst=" + dst,
		"--uid=" + itoa(os.Getuid()),
		"--gid=" + itoa(os.Getgid()),
	})
	require.Error(t, cmd.Execute())
}
