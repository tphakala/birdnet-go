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

	// Create all catalog files on disk.
	for _, f := range entry.Files {
		path := filepath.Join(subdir, f.LocalName)
		require.NoError(t, os.WriteFile(path, []byte("data"), 0o644))
	}

	mm := NewModelManager(modelsDir, nil, nil)
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

	// Verify no temp file remains.
	_, err = os.Stat(destPath + ".tmp")
	assert.True(t, os.IsNotExist(err), "temp file should be removed after successful download")

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

	// Verify temp file was cleaned up.
	_, statErr := os.Stat(destPath + ".tmp")
	assert.True(t, os.IsNotExist(statErr), "temp file should be cleaned up after checksum mismatch")

	// Verify destination file was not created.
	_, statErr = os.Stat(destPath)
	assert.True(t, os.IsNotExist(statErr), "destination file should not exist after checksum failure")
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
