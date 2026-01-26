package analysis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
)

func TestFreshInstall_CreatesDBAtConfiguredPath(t *testing.T) {
	// Reset database mode for test isolation
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	// Simulate fresh install detection
	state := datastoreV2.CheckMigrationStateBeforeStartup(settings)
	require.True(t, state.FreshInstall, "should detect fresh install")

	// Initialize fresh install
	ds, err := v2only.InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Database should be at configured path
	_, err = os.Stat(configuredPath)
	require.NoError(t, err, "database should exist at configured path")

	// No _v2 suffix database
	v2Path := filepath.Join(tmpDir, "birdnet_v2.db")
	_, err = os.Stat(v2Path)
	assert.True(t, os.IsNotExist(err), "should NOT create _v2 database")
}

func TestFreshInstall_CustomPathWithSubdirectory(t *testing.T) {
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "data", "birds", "detections.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = customPath

	// Should be detected as fresh install
	state := datastoreV2.CheckMigrationStateBeforeStartup(settings)
	require.True(t, state.FreshInstall, "should detect fresh install with custom path")

	ds, err := v2only.InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// Directory should be created
	dirInfo, err := os.Stat(filepath.Dir(customPath))
	require.NoError(t, err, "directory should be created")
	assert.True(t, dirInfo.IsDir(), "should be a directory")

	// Database at exact custom path
	_, err = os.Stat(customPath)
	require.NoError(t, err, "database should exist at custom path")
}

func TestFreshInstall_SetsEnhancedDatabaseMode(t *testing.T) {
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	// Before initialization, should not be enhanced
	assert.False(t, datastoreV2.IsEnhancedDatabase(), "should not be enhanced before init")

	ds, err := v2only.InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	defer func() { _ = ds.Close() }()

	// After initialization, should be enhanced
	assert.True(t, datastoreV2.IsEnhancedDatabase(), "should be enhanced after fresh install")
}

func TestFreshInstall_RestartDetectsExistingV2(t *testing.T) {
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	configuredPath := filepath.Join(tmpDir, "birdnet.db")

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = configuredPath

	// First: fresh install
	ds, err := v2only.InitializeFreshInstall(settings, nil)
	require.NoError(t, err)
	require.NoError(t, ds.Close())

	// Second: restart - should detect existing v2 database
	state := datastoreV2.CheckMigrationStateBeforeStartup(settings)

	// Should NOT be detected as fresh install anymore
	assert.False(t, state.FreshInstall, "should NOT be fresh install on restart")
	// Should have v2 available
	assert.True(t, state.V2Available, "v2 should be available on restart")
	// Should not require legacy
	assert.False(t, state.LegacyRequired, "legacy should not be required on restart")
}

func TestExistingLegacy_NotFreshInstall(t *testing.T) {
	datastoreV2.ResetDatabaseMode()
	defer datastoreV2.ResetDatabaseMode()

	tmpDir := t.TempDir()
	legacyPath := filepath.Join(tmpDir, "birdnet.db")

	// Create an empty file to simulate legacy database
	f, err := os.Create(legacyPath) //nolint:gosec // Test file path is safe
	require.NoError(t, err)
	require.NoError(t, f.Close())

	settings := &conf.Settings{}
	settings.Output.SQLite.Enabled = true
	settings.Output.SQLite.Path = legacyPath

	state := datastoreV2.CheckMigrationStateBeforeStartup(settings)

	// Should NOT be fresh install
	assert.False(t, state.FreshInstall, "should NOT be fresh install when legacy exists")
	// Should require legacy
	assert.True(t, state.LegacyRequired, "should require legacy when legacy exists")
}
