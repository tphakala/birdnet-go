package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test URL paths used in httptest handlers.
const (
	testPathModelONNX = "/model.onnx"
	testPathLabels    = "/labels.txt"
	testPathGeomodel  = "/geomodel.onnx"
	testPathGeoLabels = "/geomodel_labels.txt"
)

// sha256Hex returns the hex-encoded SHA-256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

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

	mm := NewModelManager(modelsDir, nil, nil)
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

	mm := NewModelManager(t.TempDir(), nil, nil)
	assert.False(t, mm.IsInstalled("battybirdnet-eu"), "empty manager should report nothing installed")
	assert.False(t, mm.IsInstalled("nonexistent"), "unknown ID should not be installed")
}

func TestModelManager_ListInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)
	installed := mm.ListInstalled()
	assert.Empty(t, installed, "empty manager should return empty slice")
	// Verify it returns a non-nil slice so JSON serialization produces [].
	require.NotNil(t, installed)
}

func TestModelManager_UninstallRejectsPermanent(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)

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

	mm := NewModelManager(t.TempDir(), nil, nil)

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

	// Create all catalog files on disk in their expected locations.
	for _, f := range entry.Files {
		var dir string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			dir = filepath.Join(modelsDir, "shared")
		} else {
			dir = subdir
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID))

	require.NoError(t, mm.Uninstall(entry.ID))
	assert.False(t, mm.IsInstalled(entry.ID))

	// Model file should be gone, labels should remain,
	// shared geomodel files should be gone (no other dependent model installed).
	for _, f := range entry.Files {
		var path string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, err := os.Stat(path)
		switch f.Role {
		case RoleModel:
			assert.True(t, os.IsNotExist(err), "model file %s should be deleted", f.LocalName)
		case RoleLabels:
			require.NoError(t, err, "labels file %s should be retained", f.LocalName)
		case RoleGeomodel:
			assert.True(t, os.IsNotExist(err), "geomodel file %s should be deleted when no dependents remain", f.LocalName)
		}
	}
}

func TestModelManager_UninstallUnknownCatalogID(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)
	err := mm.Uninstall("completely-unknown-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown catalog ID")
}

func TestModelManager_GetDownloadState_Nil(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)
	state := mm.GetDownloadState("battybirdnet-eu")
	assert.Nil(t, state, "should return nil when no download is in progress")
}

func TestModelManager_DownloadFile(t *testing.T) {
	t.Parallel()

	content := []byte("fake model data for download test")
	checksum := sha256Hex(content)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	mm := NewModelManager(t.TempDir(), nil, nil)
	destPath := filepath.Join(mm.modelsDir, "test-model", "model.onnx")

	mm.downloading["test-download"] = &DownloadState{CatalogID: "test-download", Status: StatusDownloading}
	err := mm.downloadFile("test-download", srv.URL+"/model.onnx", destPath, checksum, int64(len(content)), 0)
	require.NoError(t, err)

	// Verify file was written with correct content.
	got, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, got)

	// Verify no temp files remain.
	matches, _ := filepath.Glob(destPath + ".*.tmp")
	assert.Empty(t, matches, "temp files should be removed after successful download")

	// Verify progress was updated in shared state.
	state := mm.GetDownloadState("test-download")
	require.NotNil(t, state)
	assert.Equal(t, int64(len(content)), state.DownloadedBytes)
}

func TestModelManager_DownloadFile_BadChecksum(t *testing.T) {
	t.Parallel()

	content := []byte("some file content")
	wrongChecksum := "0000000000000000000000000000000000000000000000000000000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	mm := NewModelManager(t.TempDir(), nil, nil)
	destPath := filepath.Join(mm.modelsDir, "bad-checksum", "model.onnx")

	mm.downloading["test-bad-checksum"] = &DownloadState{CatalogID: "test-bad-checksum", Status: StatusDownloading}
	err := mm.downloadFile("test-bad-checksum", srv.URL+"/model.onnx", destPath, wrongChecksum, int64(len(content)), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum")

	// Verify temp files were cleaned up.
	matches, _ := filepath.Glob(destPath + ".*.tmp")
	assert.Empty(t, matches, "temp files should be cleaned up after checksum mismatch")

	// Verify destination file was not created.
	_, err = os.Stat(destPath)
	assert.True(t, os.IsNotExist(err), "destination file should not exist after checksum failure")
}

func TestModelManager_Install(t *testing.T) {
	t.Parallel()

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\nspecies_c\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models/test.onnx":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(modelContent)
		case "/models/labels.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-install-model",
		Name:            "Test Model",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: labelsChecksum, SizeBytes: int64(len(labelsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	progress := make(chan DownloadState, 100)
	err := mm.Install(&entry, srv.URL, progress)
	require.NoError(t, err)

	// Verify installed.
	assert.True(t, mm.IsInstalled("test-install-model"))

	// Verify files exist with correct content.
	gotModel, err := os.ReadFile(filepath.Join(modelsDir, "test-install-model", "test.onnx"))
	require.NoError(t, err)
	assert.Equal(t, modelContent, gotModel)

	gotLabels, err := os.ReadFile(filepath.Join(modelsDir, "test-install-model", "labels.txt"))
	require.NoError(t, err)
	assert.Equal(t, labelsContent, gotLabels)

	// Verify final complete status was sent.
	close(progress)
	var foundComplete bool
	for s := range progress {
		if s.Status == StatusComplete {
			foundComplete = true
		}
	}
	assert.True(t, foundComplete, "expected a 'complete' progress status")
}

func TestModelManager_Install_AlreadyInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)

	// Manually mark as installed.
	mm.mu.Lock()
	mm.installed["test-already"] = InstalledModel{
		CatalogID: "test-already",
		ModelPath: "/fake/path/model.onnx",
	}
	mm.mu.Unlock()

	entry := CatalogEntry{
		ID:   "test-already",
		Name: "Already Installed",
	}

	err := mm.Install(&entry, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already installed")
}

func TestModelManager_Install_SharedEmbeddings(t *testing.T) {
	t.Parallel()

	modelContent := []byte("bat-model-data")
	embeddingsContent := []byte("shared-embeddings-data")
	modelChecksum := sha256Hex(modelContent)
	embeddingsChecksum := sha256Hex(embeddingsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bat_model.onnx":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(modelContent)
		case "/embeddings.onnx":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(embeddingsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-bat-shared",
		Name:            "Test Bat Model",
		Category:        CategoryBat,
		Version:         "1.0",
		HuggingFaceRepo: "test/bat-repo",
		Files: []CatalogFile{
			{RemotePath: "bat_model.onnx", LocalName: "bat_model.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "embeddings.onnx", LocalName: "embeddings.onnx", Role: RoleEmbeddings, SHA256: embeddingsChecksum, SizeBytes: int64(len(embeddingsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	err := mm.Install(&entry, srv.URL, nil)
	require.NoError(t, err)

	// Embeddings should be in shared/, not in the model subdirectory.
	sharedPath := filepath.Join(modelsDir, "shared", "embeddings.onnx")
	_, err = os.Stat(sharedPath)
	require.NoError(t, err, "embeddings file should exist in shared/ directory")

	modelSubdirPath := filepath.Join(modelsDir, "test-bat-shared", "embeddings.onnx")
	_, err = os.Stat(modelSubdirPath)
	assert.True(t, os.IsNotExist(err), "embeddings file should NOT exist in model subdirectory")

	// Model file should be in the model subdirectory.
	modelPath := filepath.Join(modelsDir, "test-bat-shared", "bat_model.onnx")
	gotModel, err := os.ReadFile(modelPath)
	require.NoError(t, err)
	assert.Equal(t, modelContent, gotModel)
}

func TestModelManager_Install_ConcurrentDownloadRejected(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)

	// Manually mark a model as currently downloading.
	mm.mu.Lock()
	mm.downloading["test-concurrent"] = &DownloadState{
		CatalogID: "test-concurrent",
		Status:    StatusDownloading,
	}
	mm.mu.Unlock()

	entry := CatalogEntry{
		ID:   "test-concurrent",
		Name: "Concurrent Test",
	}

	err := mm.Install(&entry, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already being downloaded")
}

func TestModelManager_UninstallSucceedsWhenModelNotLoaded(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.RegistryID, "perch-v2 must have a RegistryID for this test")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	// Create all catalog files on disk in their expected locations.
	for _, f := range entry.Files {
		var dir string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			dir = filepath.Join(modelsDir, "shared")
		} else {
			dir = subdir
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	// Orchestrator with empty models map: IsModelLoaded returns false,
	// so Uninstall skips the unload step and proceeds to delete files.
	orch := &Orchestrator{
		models: make(map[string]*modelEntry),
	}

	mm := NewModelManager(modelsDir, orch, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID), "model must be installed before uninstall")

	err := mm.Uninstall(entry.ID)
	require.NoError(t, err, "Uninstall must succeed when model is not loaded")
	assert.False(t, mm.IsInstalled(entry.ID), "model must be removed from installed map")

	// Verify per-role file expectations after uninstall.
	for _, f := range entry.Files {
		var path string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, statErr := os.Stat(path)
		switch f.Role {
		case RoleModel, RoleData:
			assert.True(t, os.IsNotExist(statErr), "%s file %s must be deleted after uninstall", f.Role, f.LocalName)
		case RoleLabels:
			require.NoError(t, statErr, "labels file %s must be retained after uninstall", f.LocalName)
		case RoleGeomodel:
			assert.True(t, os.IsNotExist(statErr), "geomodel file %s must be deleted when no dependents remain", f.LocalName)
		}
	}
}

func TestModelManager_UninstallAbortsOnUnloadFailure(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.RegistryID, "perch-v2 must have a RegistryID for this test")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	for _, f := range entry.Files {
		var dir string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			dir = filepath.Join(modelsDir, "shared")
		} else {
			dir = subdir
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	// Orchestrator with the model present in the models map AND set as
	// primary. IsModelLoaded returns true, but UnloadModel refuses to
	// unload the primary model, simulating a "model still in use" failure.
	primaryBN := &BirdNET{ModelInfo: ModelInfo{ID: entry.RegistryID}}
	orch := &Orchestrator{
		models: map[string]*modelEntry{
			entry.RegistryID: {instance: primaryBN},
		},
		primary: primaryBN,
	}

	mm := NewModelManager(modelsDir, orch, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID), "model must be installed before uninstall attempt")

	err := mm.Uninstall(entry.ID)
	require.Error(t, err, "Uninstall must return an error when UnloadModel fails")
	assert.Contains(t, err.Error(), "model still in use")

	assert.True(t, mm.IsInstalled(entry.ID), "model must remain installed after failed uninstall")

	// All files must still exist on disk.
	for _, f := range entry.Files {
		var path string
		if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, statErr := os.Stat(path)
		assert.NoError(t, statErr, "file %s must still exist after aborted uninstall", f.LocalName)
	}
}

func TestModelManager_Install_SharedGeomodel(t *testing.T) {
	t.Parallel()

	modelContent := []byte("perch-model-data")
	labelsContent := []byte("species_a\nspecies_b\n")
	geomodelContent := []byte("geomodel-onnx-data")
	geomodelLabelsContent := []byte("Acrocephalus_arundinaceus\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)
	geomodelChecksum := sha256Hex(geomodelContent)
	geomodelLabelsChecksum := sha256Hex(geomodelLabelsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelONNX:
			_, _ = w.Write(modelContent)
		case testPathLabels:
			_, _ = w.Write(labelsContent)
		case testPathGeomodel:
			_, _ = w.Write(geomodelContent)
		case testPathGeoLabels:
			_, _ = w.Write(geomodelLabelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-geomodel-shared",
		Name:            "Test with Geomodel",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: labelsChecksum, SizeBytes: int64(len(labelsContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodel, SHA256: geomodelChecksum, SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodel, SHA256: geomodelLabelsChecksum, SizeBytes: int64(len(geomodelLabelsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	err := mm.Install(&entry, srv.URL, nil)
	require.NoError(t, err)

	// Geomodel files should be in shared/, not in the model subdirectory.
	sharedONNX := filepath.Join(modelsDir, "shared", "geomodel_v3.onnx")
	_, err = os.Stat(sharedONNX)
	require.NoError(t, err, "geomodel ONNX should exist in shared/")

	sharedLabels := filepath.Join(modelsDir, "shared", "geomodel_v3_labels.txt")
	_, err = os.Stat(sharedLabels)
	require.NoError(t, err, "geomodel labels should exist in shared/")

	// Model file should be in the model subdirectory.
	modelPath := filepath.Join(modelsDir, "test-geomodel-shared", "model.onnx")
	_, err = os.Stat(modelPath)
	require.NoError(t, err, "model file should exist in model subdirectory")

	// Geomodel files should NOT be in the model subdirectory.
	_, err = os.Stat(filepath.Join(modelsDir, "test-geomodel-shared", "geomodel_v3.onnx"))
	assert.True(t, os.IsNotExist(err), "geomodel should NOT exist in model subdirectory")
}

func TestModelManager_Install_GeomodelSkipsExisting(t *testing.T) {
	t.Parallel()

	modelContent := []byte("model-data-second")
	geomodelContent := []byte("shared-geomodel-data")
	modelChecksum := sha256Hex(modelContent)
	geomodelChecksum := sha256Hex(geomodelContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelONNX:
			_, _ = w.Write(modelContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	modelsDir := t.TempDir()

	// Pre-create the shared geomodel file (simulating a previous install).
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	sharedPath := filepath.Join(sharedDir, "geomodel.onnx")
	require.NoError(t, os.WriteFile(sharedPath, geomodelContent, 0o644))

	entry := CatalogEntry{
		ID:              "test-skip-geomodel",
		Name:            "Second Model with Shared Geomodel",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geomodel.onnx", Role: RoleGeomodel, SHA256: geomodelChecksum, SizeBytes: int64(len(geomodelContent))},
		},
	}

	mm := NewModelManager(modelsDir, nil, nil)
	err := mm.Install(&entry, srv.URL, nil)
	require.NoError(t, err)

	// The server returns 404 for geomodel.onnx, so if Install tried to
	// download it, it would fail. Success proves it was skipped.
	assert.True(t, mm.IsInstalled("test-skip-geomodel"))

	// Verify shared file is still there with original content.
	got, err := os.ReadFile(sharedPath)
	require.NoError(t, err)
	assert.Equal(t, geomodelContent, got)
}

func TestModelManager_Uninstall_GeomodelRetainedWhenDependentExists(t *testing.T) {
	t.Parallel()

	// Use real catalog entries: both perch-v2 and birdnet-v3.0 have geomodel files.
	entryPerch, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	entryV3, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	// Set up files on disk for both entries.
	for _, entry := range []CatalogEntry{entryPerch, entryV3} {
		subdir := filepath.Join(modelsDir, entry.ID)
		require.NoError(t, os.MkdirAll(subdir, 0o755))
		for _, f := range entry.Files {
			var dir string
			if f.Role == RoleEmbeddings || f.Role == RoleGeomodel {
				dir = sharedDir
			} else {
				dir = subdir
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
		}
	}

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled("perch-v2"))
	require.True(t, mm.IsInstalled("birdnet-v3.0"))

	// Uninstall perch-v2; birdnet-v3.0 still depends on the geomodel.
	require.NoError(t, mm.Uninstall("perch-v2"))

	// Shared geomodel files should be retained.
	for _, f := range entryPerch.Files {
		if f.Role == RoleGeomodel {
			path := filepath.Join(sharedDir, f.LocalName)
			_, err := os.Stat(path)
			require.NoError(t, err, "geomodel file %s must be retained while birdnet-v3.0 is installed", f.LocalName)
		}
	}

	// Now uninstall birdnet-v3.0; no dependents remain.
	require.NoError(t, mm.Uninstall("birdnet-v3.0"))

	// Shared geomodel files should now be deleted.
	for _, f := range entryV3.Files {
		if f.Role == RoleGeomodel {
			path := filepath.Join(sharedDir, f.LocalName)
			_, err := os.Stat(path)
			assert.True(t, os.IsNotExist(err), "geomodel file %s must be deleted when no dependents remain", f.LocalName)
		}
	}
}

func TestModelManager_Install_PerFileHuggingFaceRepo(t *testing.T) {
	t.Parallel()

	entry := CatalogEntry{
		ID:              "test-per-file-repo",
		Name:            "Per-file repo test",
		Version:         "1.0",
		HuggingFaceRepo: "main-repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel},
			{RemotePath: "companion.bin", LocalName: "companion.bin", Role: RoleGeomodel, HuggingFaceRepo: "companion-repo"},
		},
	}

	// Verify the URL construction logic: when HuggingFaceRepo is set on a
	// CatalogFile, Install should use it instead of the entry-level repo.
	for _, f := range entry.Files {
		repo := entry.HuggingFaceRepo
		if f.HuggingFaceRepo != "" {
			repo = f.HuggingFaceRepo
		}
		got := buildHuggingFaceURL(repo, f.RemotePath)
		if f.HuggingFaceRepo != "" {
			assert.Contains(t, got, "companion-repo", "file with per-file repo should use companion-repo")
			assert.Equal(t, "https://huggingface.co/companion-repo/resolve/main/companion.bin", got)
		} else {
			assert.Contains(t, got, "main-repo", "file without per-file repo should use entry repo")
			assert.Equal(t, "https://huggingface.co/main-repo/resolve/main/model.onnx", got)
		}
	}
}

func TestModelManager_Install_GeomodelConfigWiring(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.

	modelContent := []byte("perch-model")
	labelsContent := []byte("labels")
	geomodelContent := []byte("geo-onnx")
	geomodelLabelsContent := []byte("geo-labels")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelONNX:
			_, _ = w.Write(modelContent)
		case testPathLabels:
			_, _ = w.Write(labelsContent)
		case testPathGeomodel:
			_, _ = w.Write(geomodelContent)
		case testPathGeoLabels:
			_, _ = w.Write(geomodelLabelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-geo-config",
		Name:            "Config Wiring Test",
		Version:         "1.0",
		RegistryID:      RegistryIDPerchV2,
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: sha256Hex(modelContent), SizeBytes: int64(len(modelContent))},
			{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labelsContent), SizeBytes: int64(len(labelsContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodel, SHA256: sha256Hex(geomodelContent), SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodel, SHA256: sha256Hex(geomodelLabelsContent), SizeBytes: int64(len(geomodelLabelsContent))},
		},
	}

	// Save original settings to restore after test.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	modelsDir := t.TempDir()
	settings := conf.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	err := mm.Install(&entry, srv.URL, nil)
	require.NoError(t, err)

	// Verify range filter config was set.
	current := conf.GetSettings()
	assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model)
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geomodel_v3.onnx"), current.BirdNET.RangeFilter.ModelPath)
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geomodel_v3_labels.txt"), current.BirdNET.RangeFilter.LabelsPath)
}

func TestHasGeomodelFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		entry CatalogEntry
		want  bool
	}{
		{
			name:  "no files",
			entry: CatalogEntry{Files: nil},
			want:  false,
		},
		{
			name: "model and labels only",
			entry: CatalogEntry{Files: []CatalogFile{
				{Role: RoleModel},
				{Role: RoleLabels},
			}},
			want: false,
		},
		{
			name: "has geomodel files",
			entry: CatalogEntry{Files: []CatalogFile{
				{Role: RoleModel},
				{Role: RoleGeomodel},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, hasGeomodelFiles(&tt.entry))
		})
	}
}

func TestCatalog_GeomodelFilesOnPerchAndBirdNET(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"perch-v2", "birdnet-v3.0"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			entry, ok := GetCatalogEntry(id)
			require.True(t, ok, "expected %s catalog entry to exist", id)

			assert.True(t, hasGeomodelFiles(&entry), "%s should have geomodel files", id)

			var geoFileCount int
			for _, f := range entry.Files {
				if f.Role == RoleGeomodel {
					geoFileCount++
					assert.NotEmpty(t, f.SHA256, "geomodel file %s must have a SHA256 checksum", f.LocalName)
					assert.Positive(t, f.SizeBytes, "geomodel file %s must have a non-zero size", f.LocalName)
					assert.Equal(t, geomodelHuggingFaceRepo, f.HuggingFaceRepo, "geomodel file %s must use the geomodel HuggingFace repo", f.LocalName)
				}
			}
			assert.Equal(t, 2, geoFileCount, "expected exactly 2 geomodel files (ONNX + labels)")
		})
	}
}

func TestBuildHuggingFaceURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		repo     string
		filePath string
		want     string
	}{
		{
			name:     "simple file",
			repo:     "tphakala/BirdNET-v3.0",
			filePath: "birdnet_v3.0.onnx",
			want:     "https://huggingface.co/tphakala/BirdNET-v3.0/resolve/main/birdnet_v3.0.onnx",
		},
		{
			name:     "nested path",
			repo:     "tphakala/BattyBirdNET-onnx",
			filePath: "fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
			want:     "https://huggingface.co/tphakala/BattyBirdNET-onnx/resolve/main/fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildHuggingFaceURL(tt.repo, tt.filePath)
			assert.Equal(t, tt.want, got)
		})
	}
}
