package datastore

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestCreateBackup_FilePermissions(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("fake db content"), 0o600))

	store := &SQLiteStore{
		Settings: &conf.Settings{},
	}

	err := store.createBackup(dbPath)
	require.NoError(t, err)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	var backupPath string
	for _, e := range entries {
		if e.Name() != "test.db" {
			backupPath = filepath.Join(tmpDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, backupPath, "backup file should exist")

	info, err := os.Stat(backupPath)
	require.NoError(t, err)
	// Windows does not represent Unix permission bits: os.Stat reports 0666 for
	// regular files regardless of the mode passed to OpenFile. The backup is
	// still created with 0o600 in production; the bits just are not observable
	// here. Skip the perm assertion on Windows; the rest of the test still runs.
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
			"backup file should have 0600 permissions")
	}
}

func TestCheckWritePermission_NoSymlinkFollow(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Pre-create a symlink at the old predictable path
	oldPredictablePath := filepath.Join(tmpDir, ".tmp_write_test")
	targetPath := filepath.Join(tmpDir, "symlink_target")
	require.NoError(t, os.Symlink(targetPath, oldPredictablePath))

	err := checkWritePermission(dbPath)
	require.NoError(t, err)

	// The symlink target should NOT have been created
	_, statErr := os.Stat(targetPath)
	assert.True(t, os.IsNotExist(statErr),
		"symlink target should not be created; function should use random temp name")
}

func TestCheckWritePermission_CleansUpTempFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	err := checkWritePermission(dbPath)
	require.NoError(t, err)

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "temp file should be cleaned up after permission check")
}

func TestCheckWritePermission_FailsOnReadOnlyDir(t *testing.T) {
	t.Parallel()
	// chmod cannot make a directory unwritable for its owner on Windows, so
	// the write-permission probe still succeeds and no error is returned. The
	// scenario this test asserts is only reproducible on Unix.
	if runtime.GOOS == "windows" {
		t.Skip("chmod cannot make a directory unwritable for the owner on Windows")
	}
	tmpDir := t.TempDir()

	// Make directory read-only
	require.NoError(t, os.Chmod(tmpDir, 0o555))
	t.Cleanup(func() {
		_ = os.Chmod(tmpDir, 0o755)
	})

	dbPath := filepath.Join(tmpDir, "test.db")
	err := checkWritePermission(dbPath)
	assert.Error(t, err)
}

func TestOpen_ClosesPoolOnPostConnectionFailure(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a WAL-mode database, checkpoint, and remove WAL/SHM files.
	// This leaves a valid DB file that gorm.Open can reopen successfully.
	dsn := buildSQLiteDSN(dbPath, "_journal_mode=WAL&_busy_timeout=5000")
	setupDB, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err)
	setupSQL, err := setupDB.DB()
	require.NoError(t, err)
	_, err = setupSQL.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	require.NoError(t, err)
	require.NoError(t, setupSQL.Close())
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	// Make the DB file read-only. gorm.Open will succeed (SQLite can open
	// a read-only WAL DB if the directory is writable for the new WAL file),
	// but performAutoMigration will fail with "attempt to write a readonly
	// database". This exercises the defer cleanup path that closes sqlDB
	// and nils s.DB.
	require.NoError(t, os.Chmod(dbPath, 0o444))
	t.Cleanup(func() {
		_ = os.Chmod(dbPath, 0o644)
	})

	settings := &conf.Settings{}
	settings.Output.SQLite.Path = dbPath

	store := &SQLiteStore{
		Settings: settings,
	}

	openErr := store.Open()
	require.Error(t, openErr, "Open should fail when DB file is read-only")
	assert.Nil(t, store.DB, "store.DB should be nil after Open() failure (pool closed by defer)")
}
