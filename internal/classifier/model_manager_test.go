package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelManager_ScanInstalled(t *testing.T) {
	t.Parallel()

	// Pick a known catalog entry to simulate an installed model.
	entry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok, "expected battybirdnet-eu catalog entry to exist")

	// Find the model file name from the catalog entry.
	var modelFileName string
	for _, f := range entry.Files {
		if f.Role == "model" {
			modelFileName = f.LocalName
			break
		}
	}
	require.NotEmpty(t, modelFileName, "catalog entry must have a file with role \"model\"")

	// Create a temp directory structure: <modelsDir>/<catalogID>/<modelFile>
	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	modelPath := filepath.Join(subdir, modelFileName)
	require.NoError(t, os.WriteFile(modelPath, []byte("fake-onnx-data"), 0o644))

	mm := NewModelManager(modelsDir, nil)
	mm.ScanInstalled()

	assert.True(t, mm.IsInstalled(entry.ID), "expected %s to be detected as installed", entry.ID)

	installed := mm.ListInstalled()
	require.Len(t, installed, 1)
	assert.Equal(t, entry.ID, installed[0].CatalogID)
	assert.Equal(t, modelPath, installed[0].ModelPath)
	assert.Equal(t, entry.Version, installed[0].Version)
}

func TestModelManager_IsInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)
	assert.False(t, mm.IsInstalled("battybirdnet-eu"), "empty manager should report nothing installed")
	assert.False(t, mm.IsInstalled("nonexistent"), "unknown ID should not be installed")
}

func TestModelManager_ListInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)
	installed := mm.ListInstalled()
	assert.Empty(t, installed, "empty manager should return empty slice")
	// Verify it returns a non-nil slice so JSON serialization produces [].
	require.NotNil(t, installed)
}

func TestModelManager_UninstallRejectsPermanent(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)

	// Find a catalog entry whose RegistryID maps to BirdNET_V2.4.
	var permanentID string
	for _, entry := range EmbeddedCatalog {
		if entry.RegistryID == permanentRegistryID {
			permanentID = entry.ID
			break
		}
	}

	// If no catalog entry maps to BirdNET_V2.4, the test is not applicable
	// (the permanent model is embedded, not downloadable). Skip gracefully.
	if permanentID == "" {
		t.Skip("no catalog entry maps to permanentRegistryID; nothing to test")
	}

	err := mm.Uninstall(permanentID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot uninstall")
}

func TestModelManager_UninstallNotInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)

	err := mm.Uninstall("battybirdnet-eu")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

func TestModelManager_UninstallRemovesModelRetainsLabels(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	// Create all catalog files on disk.
	for _, f := range entry.Files {
		path := filepath.Join(subdir, f.LocalName)
		require.NoError(t, os.WriteFile(path, []byte("data"), 0o644))
	}

	mm := NewModelManager(modelsDir, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID))

	require.NoError(t, mm.Uninstall(entry.ID))
	assert.False(t, mm.IsInstalled(entry.ID))

	// Model file should be gone, labels should remain.
	for _, f := range entry.Files {
		path := filepath.Join(subdir, f.LocalName)
		_, err := os.Stat(path)
		if f.Role == "model" {
			assert.True(t, os.IsNotExist(err), "model file %s should be deleted", f.LocalName)
		}
		if f.Role == "labels" {
			assert.NoError(t, err, "labels file %s should be retained", f.LocalName)
		}
	}
}

func TestModelManager_UninstallUnknownCatalogID(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)
	err := mm.Uninstall("completely-unknown-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown catalog ID")
}

func TestModelManager_GetDownloadState_Nil(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil)
	state := mm.GetDownloadState("battybirdnet-eu")
	assert.Nil(t, state, "should return nil when no download is in progress")
}
