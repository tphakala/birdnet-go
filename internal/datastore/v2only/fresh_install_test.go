package v2only

import (
	"fmt"
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
	"github.com/tphakala/birdnet-go/internal/suncalc"
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

// TestInitializeFreshInstall_ConfiguresSunCalc pins that a fresh-install datastore gets a sun
// calculator for the configured station location. Without it civil dawn is never available, so
// the dawn-chorus onset endpoint returns a null onset for every day and the chart stays empty.
func TestInitializeFreshInstall_ConfiguresSunCalc(t *testing.T) {
	v2.ResetDatabaseMode()
	t.Cleanup(v2.ResetDatabaseMode)

	const (
		// Mid-latitude station: civil dawn is defined on every day of the year.
		stationLatitude  = 50.0
		stationLongitude = 10.0
		onsetDate        = "2026-05-15"
		minuteStep       = 5
	)

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet.db")
	settings.BirdNET.Latitude = stationLatitude
	settings.BirdNET.Longitude = stationLongitude

	// The sun calculator reads the published settings snapshot, so publish this test's settings
	// (and restore the previous snapshot) rather than depend on whatever another test left.
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })
	conf.StoreSettings(settings)

	ds, err := InitializeFreshInstall(settings, nil, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	require.NotNil(t, ds.suncalc, "fresh install must configure a sun calculator")

	// Enough morning detections for a day to qualify for an onset.
	for i := range minOnsetDetections {
		require.NoError(t, ds.Save(&datastore.Note{
			Date:           onsetDate,
			Time:           fmt.Sprintf("05:%02d:00", i*minuteStep),
			ScientificName: "Turdus merula",
			CommonName:     "Eurasian Blackbird",
			Confidence:     0.9,
		}, nil))
	}

	got, err := ds.GetDailyActivityOnset(t.Context(), onsetDate, onsetDate, "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, minOnsetDetections, got[0].DetectionCount)
	assert.NotNil(t, got[0].OnsetRelMinutes, "onset relative to civil dawn must be computed")
}

// TestInitializeFreshInstall_SunCalcFollowsLocationChange pins that the datastore's sun
// calculator follows a station location changed through the settings (a new published snapshot)
// without rebuilding the datastore, so time-of-day and dawn-chorus onset use the new location.
// It publishes global settings, so it must not run in parallel.
func TestInitializeFreshInstall_SunCalcFollowsLocationChange(t *testing.T) {
	v2.ResetDatabaseMode()
	t.Cleanup(v2.ResetDatabaseMode)
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	const (
		helsinkiLatitude  = 60.1699
		helsinkiLongitude = 24.9384
		sydneyLatitude    = -33.8688
		sydneyLongitude   = 151.2093
	)

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = filepath.Join(t.TempDir(), "birdnet.db")
	settings.BirdNET.Latitude = helsinkiLatitude
	settings.BirdNET.Longitude = helsinkiLongitude
	conf.StoreSettings(settings)

	ds, err := InitializeFreshInstall(settings, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, ds.Close()) })

	require.NotNil(t, ds.suncalc)
	assert.Equal(t, "Europe/Helsinki", ds.suncalc.LocationName())

	moved := conf.CloneSettings(settings)
	moved.BirdNET.Latitude = sydneyLatitude
	moved.BirdNET.Longitude = sydneyLongitude
	conf.StoreSettings(moved)

	// The sun times the datastore classifies with must be the new location's. Checked before
	// LocationName, so the sun-times call alone must notice the change.
	date := time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC)
	got, err := ds.suncalc.GetSunEventTimes(date)
	require.NoError(t, err)
	want, err := suncalc.NewSunCalc(sydneyLatitude, sydneyLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, want.Sunrise.Equal(got.Sunrise), "sunrise must match a fixed Sydney calculator")
	assert.True(t, want.CivilDawn.Equal(got.CivilDawn), "civil dawn must match a fixed Sydney calculator")

	assert.Equal(t, "Australia/Sydney", ds.suncalc.LocationName(),
		"the datastore's sun calculator must follow the published station location")
}
