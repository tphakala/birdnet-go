package v2only

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestInitializeFreshInstall_SQLite(t *testing.T) {
	// Reset database mode for test isolation
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = dbPath

	ds, err := InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Verify database was created at configured path (NOT birdnet_v2.db)
	_, err = os.Stat(dbPath)
	require.NoError(t, err, "database should exist at configured path")

	// Verify no _v2 database was created
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")
	_, err = os.Stat(v2Path)
	assert.True(t, os.IsNotExist(err), "should NOT create _v2 database for fresh install")

	// Verify enhanced database mode was set
	assert.True(t, v2.IsEnhancedDatabase(), "enhanced database flag should be set")
}

func TestInitializeFreshInstall_SQLite_CustomPath(t *testing.T) {
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom", "birds", "detections.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = customPath

	ds, err := InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Verify database was created at custom path
	_, err = os.Stat(customPath)
	require.NoError(t, err, "database should exist at custom path")

	// Verify parent directories were created
	dirInfo, err := os.Stat(filepath.Dir(customPath))
	require.NoError(t, err)
	assert.True(t, dirInfo.IsDir())
}

func TestInitializeFreshInstall_MigrationStateCompleted(t *testing.T) {
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = dbPath

	ds, err := InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Check the migration state by re-checking startup state
	// The state should be COMPLETED
	state := v2.CheckMigrationStateBeforeStartup(settings)
	assert.Equal(t, entities.MigrationStatusCompleted, state.MigrationStatus,
		"migration status should be COMPLETED for fresh install")
	assert.True(t, state.V2Available, "v2 should be available")
	assert.False(t, state.LegacyRequired, "legacy should not be required")
	assert.False(t, state.FreshInstall, "should not be detected as fresh install after initialization")
}

func TestInitializeFreshInstall_CanSaveAndRetrieve(t *testing.T) {
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = dbPath

	ds, err := InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Save a detection
	note := &datastore.Note{
		Date:           "2024-01-26",
		Time:           "12:00:00",
		ScientificName: "Turdus merula",
		Confidence:     0.95,
	}
	err = ds.Save(note, nil)
	require.NoError(t, err)

	// Retrieve it
	retrieved, err := ds.GetLastDetections(1)
	require.NoError(t, err)
	require.Len(t, retrieved, 1)
	assert.Equal(t, "Turdus merula", retrieved[0].ScientificName)
}

func TestInitializeFreshInstall_NoDatabase(t *testing.T) {
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	settings := &conf.Settings{}
	// No database configured

	_, err := InitializeFreshInstall(settings, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no database configured")
}
