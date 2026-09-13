package v2only

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	ds, err := InitializeFreshInstall(settings, nil, nil)
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

	ds, err := InitializeFreshInstall(settings, nil, nil)
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

	ds, err := InitializeFreshInstall(settings, nil, nil)
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

	ds, err := InitializeFreshInstall(settings, nil, nil)
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

	_, err := InitializeFreshInstall(settings, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no database configured")
}

// TestInitializeFreshInstall_WiresSunCalcForTimeOfDay is a regression test for the Search
// page's "Time of Day" column always showing "any" regardless of the actual detection
// timestamp. The root cause was that InitializeFreshInstall (and initializeV2OnlyMode in
// internal/analysis/database_migration.go) built the v2only.Config without a SunCalc
// instance, so calculateTimeOfDay always hit its ds.suncalc == nil guard and returned the
// literal "any" for every detection, no matter when it occurred.
func TestInitializeFreshInstall_WiresSunCalcForTimeOfDay(t *testing.T) {
	v2.ResetDatabaseMode()
	t.Cleanup(v2.ResetDatabaseMode)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "birdnet.db")

	// Helsinki coordinates, used elsewhere in this codebase's suncalc tests.
	const testLatitude = 60.1699
	const testLongitude = 24.9384

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = dbPath
	settings.BirdNET.Latitude = testLatitude
	settings.BirdNET.Longitude = testLongitude

	ds, err := InitializeFreshInstall(settings, nil, nil)
	require.NoError(t, err)
	// Cleanups run LIFO, so this closes the datastore before ResetDatabaseMode
	// runs. The close error is asserted rather than discarded: silently leaking
	// the SQLite handle would make later tests in this package flaky.
	t.Cleanup(func() {
		require.NoError(t, ds.Close())
	})

	require.NotNil(t, ds.suncalc,
		"InitializeFreshInstall must wire a SunCalc instance so calculateTimeOfDay can classify "+
			"by the configured station coordinates instead of always falling back to \"any\"")

	// A well-into-daytime and a well-into-nighttime UTC timestamp (mid-September, outside the
	// midnight-sun/polar-night window) at these coordinates. The exact classification (day vs.
	// night) is covered by internal/suncalc's own tests; here we only need to confirm the
	// datastore no longer short-circuits to the "any" fallback now that SunCalc is configured.
	day := time.Date(2024, 9, 15, 12, 0, 0, 0, time.UTC)
	night := time.Date(2024, 9, 15, 0, 0, 0, 0, time.UTC)

	assert.NotEqual(t, datastore.TimeOfDayAny, ds.calculateTimeOfDay(day, testLatitude, testLongitude),
		"daytime detection must be classified, not the \"any\" fallback")
	assert.NotEqual(t, datastore.TimeOfDayAny, ds.calculateTimeOfDay(night, testLatitude, testLongitude),
		"nighttime detection must be classified, not the \"any\" fallback")
}

func TestInitializeFreshInstall_EmptySQLitePath(t *testing.T) {
	v2.ResetDatabaseMode()
	defer v2.ResetDatabaseMode()

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = "" // Empty path

	_, err := InitializeFreshInstall(settings, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sqlite path is empty")
}
