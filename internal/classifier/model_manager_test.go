package classifier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Test URL paths used in httptest handlers.
const (
	testPathModelONNX    = "/model.onnx"
	testPathLabels       = "/labels.txt"
	testPathGeomodel     = "/geomodel.onnx"
	testPathGeoLabels    = "/geomodel_labels.txt"
	testPathModelsONNX   = "/models/test.onnx"
	testPathModelsLabels = "/models/labels.txt"
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

	// The permanent BirdNET v2.4 baseline is always reported installed, so the bat
	// model is looked up by id rather than by position.
	im := installedByID(t, mm, entry.ID)
	assert.Equal(t, entry.ID, im.CatalogID)
	assert.Equal(t, modelPath, im.ModelPath)
	assert.Equal(t, entry.Version, im.Version)

	// The built-in v2.4 primary classifier is always installed (embedded baseline).
	assert.True(t, mm.IsInstalled("birdnet-v2.4"), "built-in BirdNET v2.4 must always be installed")
	v24 := installedByID(t, mm, "birdnet-v2.4")
	assert.Equal(t, "builtin", v24.VariantID, "v2.4 baseline variant id")
	assert.Empty(t, v24.ModelPath, "v2.4 baseline has no model path (embedded)")
}

// variantByID returns the variant with the given id from entry, failing the
// test if it is absent.
func variantByID(t *testing.T, entry *CatalogEntry, id string) *CatalogVariant {
	t.Helper()
	for i := range entry.Variants {
		if entry.Variants[i].ID == id {
			return &entry.Variants[i]
		}
	}
	t.Fatalf("variant %q not found in entry %s", id, entry.ID)
	return nil
}

// modelRoleLocalName returns the LocalName of the RoleModel file in files.
func modelRoleLocalName(t *testing.T, files []CatalogFile) string {
	t.Helper()
	for _, f := range files {
		if f.Role == RoleModel {
			return f.LocalName
		}
	}
	t.Fatalf("no model-role file found in %d files", len(files))
	return ""
}

// installedByID returns the InstalledModel recorded for catalogID, failing the
// test if it is not present in the installed list.
func installedByID(t *testing.T, mm *ModelManager, catalogID string) InstalledModel {
	t.Helper()
	for _, im := range mm.ListInstalled() {
		if im.CatalogID == catalogID {
			return im
		}
	}
	t.Fatalf("model %s not found in installed list", catalogID)
	return InstalledModel{}
}

// writeVariantModelFile writes a placeholder model file for the given variant of
// a catalog entry into its on-disk subdirectory, mimicking an install of that
// specific variant. It returns the full path to the written model file.
func writeVariantModelFile(t *testing.T, modelsDir string, entry *CatalogEntry, variantID string) string {
	t.Helper()
	v := variantByID(t, entry, variantID)
	modelName := modelRoleLocalName(t, v.Files)
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	modelPath := filepath.Join(subdir, modelName)
	require.NoError(t, os.WriteFile(modelPath, []byte("fake-onnx-data"), 0o600))
	return modelPath
}

func TestModelManager_ScanInstalled_DetectsDefaultVariant(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.Variants, "perch-v2 must be a variant entry")

	modelsDir := t.TempDir()
	modelPath := writeVariantModelFile(t, modelsDir, &entry, "fp32")

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()

	require.True(t, mm.IsInstalled(entry.ID), "default variant install should be detected")
	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "fp32", got.VariantID, "default variant id should be recorded")
	assert.Equal(t, modelPath, got.ModelPath, "model path should point at the default variant file")
}

func TestModelManager_ScanInstalled_DetectsNonDefaultVariant(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")

	// Only the non-default int8-arm variant's model file exists on disk. The
	// default variant's filename is absent, so a default-only scan would miss it.
	modelsDir := t.TempDir()
	modelPath := writeVariantModelFile(t, modelsDir, &entry, "int8-arm")

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()

	require.True(t, mm.IsInstalled(entry.ID), "non-default variant install should be detected")
	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "int8-arm", got.VariantID, "installed variant id should be the on-disk variant")
	assert.Equal(t, modelPath, got.ModelPath, "model path should point at the installed variant file")
}

func TestModelManager_ScanInstalled_ZeroVariantEntryHasEmptyVariantID(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok, "expected battybirdnet-eu catalog entry to exist")
	require.Empty(t, entry.Variants, "battybirdnet-eu is expected to be a flat, zero-variant entry")

	modelFileName := modelRoleLocalName(t, entry.Files)
	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, modelFileName), []byte("fake-onnx-data"), 0o644))

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()

	require.True(t, mm.IsInstalled(entry.ID))
	got := installedByID(t, mm, entry.ID)
	assert.Empty(t, got.VariantID, "flat entries must record an empty variant id")
}

func TestModelManager_ScanInstalled_VariantEntryNoFileNotInstalled(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.Variants, "perch-v2 must be a variant entry")

	// The entry's subdirectory exists but carries no variant model file, so no
	// variant is present on disk and the entry must not be reported installed.
	modelsDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(modelsDir, entry.ID), 0o755))

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()

	assert.False(t, mm.IsInstalled(entry.ID), "a variant entry with no model file on disk must not be installed")
}

// flatServerEntry builds a synthetic flat (single-variant) catalog entry plus an
// httptest server that serves its model and labels files, for exercising the
// install download path. Returns the entry, a fresh modelsDir, and the server
// base URL. Both files carry real SHA-256 checksums so the download path's
// verification succeeds.
func flatServerEntry(t *testing.T) (entry CatalogEntry, modelsDir, srvURL string) {
	t.Helper()
	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelsONNX:
			_, _ = w.Write(modelContent)
		case testPathModelsLabels:
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	files := []CatalogFile{
		{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: sha256Hex(modelContent), SizeBytes: int64(len(modelContent))},
		{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labelsContent), SizeBytes: int64(len(labelsContent))},
	}
	entry = CatalogEntry{
		ID:              "test-preflight",
		Name:            "Test Preflight Model",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files:           files,
	}
	return entry, t.TempDir(), srv.URL
}

func TestModelManager_Install_RejectsInsufficientDiskSpace(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := flatServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)
	// Report far less free space than the download needs, so the preflight rejects.
	mm.freeSpaceFn = func(string) (uint64, error) { return 1024, nil }

	err := mm.Install(t.Context(), &entry, "", srvURL, nil)
	require.Error(t, err, "install must be rejected when free space is below the download total")
	assert.Contains(t, err.Error(), "disk space", "the error should name the disk-space shortfall")
	assert.False(t, mm.IsInstalled(entry.ID), "a preflight-rejected install must not be recorded")
	_, statErr := os.Stat(filepath.Join(modelsDir, entry.ID, "test.onnx"))
	assert.True(t, os.IsNotExist(statErr), "no model file should be written when the preflight rejects the install")

	// The rejection is surfaced through the retained download state, which is what
	// SSE pollers read, so the user learns why the install stopped.
	st := mm.GetDownloadState(entry.ID)
	require.NotNil(t, st, "a rejected install must leave a retained failed download state for SSE pollers")
	assert.Equal(t, StatusFailed, st.Status, "the retained state must be marked failed")
	assert.Contains(t, st.Error, "disk space", "the retained failure must carry the disk-space reason")
}

func TestModelManager_Install_SkipsPreflightWhenSizeUnknown(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := flatServerEntry(t)
	// A catalog that does not declare sizes (size_bytes == 0) gives the preflight
	// nothing to check, so it must fail open and let the install proceed rather
	// than reject on the bare margin. (Files still download and verify by SHA.)
	for i := range entry.Files {
		entry.Files[i].SizeBytes = 0
	}
	mm := NewModelManager(modelsDir, nil, nil)
	mm.freeSpaceFn = func(string) (uint64, error) { return 1, nil } // effectively no free space

	require.NoError(t, mm.Install(t.Context(), &entry, "", srvURL, nil))
	assert.True(t, mm.IsInstalled(entry.ID), "an unknown-size install must not be blocked by the preflight")
}

func TestModelManager_Install_DiskSpaceBoundary(t *testing.T) {
	t.Parallel()

	// The preflight proceeds when free space is exactly the required amount and
	// rejects one byte short, pinning the `>=` comparison against an off-by-one or
	// inverted-operator regression.
	tests := []struct {
		name      string
		freeDelta int64 // free space relative to the required amount
		wantErr   bool
	}{
		{name: "exactly enough", freeDelta: 0, wantErr: false},
		{name: "one byte short", freeDelta: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry, modelsDir, srvURL := flatServerEntry(t)
			var total int64
			for _, f := range entry.Files {
				total += f.SizeBytes
			}
			free := uint64(total + diskSpaceMarginBytes + tc.freeDelta)
			mm := NewModelManager(modelsDir, nil, nil)
			mm.freeSpaceFn = func(string) (uint64, error) { return free, nil }

			err := mm.Install(t.Context(), &entry, "", srvURL, nil)
			if tc.wantErr {
				require.Error(t, err, "one byte below the requirement must be rejected")
				assert.False(t, mm.IsInstalled(entry.ID))
			} else {
				require.NoError(t, err, "exactly the required free space must be accepted")
				assert.True(t, mm.IsInstalled(entry.ID))
			}
		})
	}
}

func TestModelManager_Install_SucceedsWithAmpleDiskSpace(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := flatServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)
	// Ample free space: the preflight must not block a valid install.
	mm.freeSpaceFn = func(string) (uint64, error) { return 1 << 40, nil } // 1 TiB

	require.NoError(t, mm.Install(t.Context(), &entry, "", srvURL, nil))
	assert.True(t, mm.IsInstalled(entry.ID), "install should succeed when free space exceeds the download total")
}

func TestModelManager_Install_ProceedsWhenDiskCheckErrors(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := flatServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)
	// A failing free-space probe must fail open: the install proceeds rather than
	// being blocked by an inability to measure free space.
	mm.freeSpaceFn = func(string) (uint64, error) {
		return 0, errors.Newf("simulated statfs failure").Build()
	}

	require.NoError(t, mm.Install(t.Context(), &entry, "", srvURL, nil))
	assert.True(t, mm.IsInstalled(entry.ID), "a free-space probe error must not block the install (fail open)")
}

func TestModelManager_ScanInstalled_PrunesRemovedModel(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok, "expected battybirdnet-eu catalog entry to exist")
	modelFileName := modelRoleLocalName(t, entry.Files)

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	modelPath := filepath.Join(subdir, modelFileName)
	require.NoError(t, os.WriteFile(modelPath, []byte("fake-onnx-data"), 0o644))

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID), "the model should be detected while its file is on disk")

	// Remove the model out-of-band, then rescan: the stale entry must be pruned.
	require.NoError(t, os.RemoveAll(subdir))
	mm.ScanInstalled()

	assert.False(t, mm.IsInstalled(entry.ID), "a model whose files were removed out-of-band must be pruned on rescan")
	for _, im := range mm.ListInstalled() {
		assert.NotEqual(t, entry.ID, im.CatalogID, "the pruned model must not remain in the installed list")
	}
}

func TestModelManager_Install_RecordsDefaultVariantID(t *testing.T) {
	t.Parallel()

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelsONNX:
			_, _ = w.Write(modelContent)
		case testPathModelsLabels:
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// A resolved variant entry: Files carries the default variant's files (as
	// resolveVariantDefaults would produce) and Variants names that default.
	files := []CatalogFile{
		{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
		{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: labelsChecksum, SizeBytes: int64(len(labelsContent))},
	}
	entry := CatalogEntry{
		ID:              "test-variant-install",
		Name:            "Test Variant Model",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		// Non-default variant first so this proves the install records the DEFAULT
		// variant (via Default: true), not merely the first one in the slice.
		Variants: []CatalogVariant{
			{ID: "int8-arm", Files: files},
			{ID: "fp32", Default: true, Files: files},
		},
		Files: files,
	}

	mm := NewModelManager(t.TempDir(), nil, nil)
	require.NoError(t, mm.Install(t.Context(), &entry, "", srv.URL, nil))

	require.True(t, mm.IsInstalled(entry.ID))
	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "fp32", got.VariantID, "install must record the default variant id, matching what ScanInstalled derives")
}

func TestModelManager_UninstallNonDefaultVariant(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	// Synthetic non-default install: only the int8-arm variant's model file on disk.
	modelsDir := t.TempDir()
	modelPath := writeVariantModelFile(t, modelsDir, &entry, "int8-arm")

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID))
	require.Equal(t, "int8-arm", installedByID(t, mm, entry.ID).VariantID)

	require.NoError(t, mm.Uninstall(entry.ID))
	assert.False(t, mm.IsInstalled(entry.ID))
	_, err := os.Stat(modelPath)
	assert.True(t, os.IsNotExist(err), "the installed non-default variant's model file must be deleted")
}

// twoVariantServerEntry builds a synthetic two-variant catalog entry (default
// "fp32" and non-default "int8-arm" with DISTINCT model LocalNames) plus an
// httptest server that serves each variant's files, so a test can tell which
// variant was actually downloaded. Returns the entry, a fresh modelsDir, and the
// server base URL.
func twoVariantServerEntry(t *testing.T) (entry CatalogEntry, modelsDir, srvURL string) {
	t.Helper()
	fp32Data, int8Data, labels := []byte("fp32-model"), []byte("int8-model"), []byte("a\nb\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fp32.onnx":
			_, _ = w.Write(fp32Data)
		case "/int8.onnx":
			_, _ = w.Write(int8Data)
		case "/labels.txt":
			_, _ = w.Write(labels)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	fp32Files := []CatalogFile{
		{RemotePath: "fp32.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: sha256Hex(fp32Data), SizeBytes: int64(len(fp32Data))},
		{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labels), SizeBytes: int64(len(labels))},
	}
	int8Files := []CatalogFile{
		{RemotePath: "int8.onnx", LocalName: "model_int8.onnx", Role: RoleModel, SHA256: sha256Hex(int8Data), SizeBytes: int64(len(int8Data))},
		{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labels), SizeBytes: int64(len(labels))},
	}
	entry = CatalogEntry{
		ID:              "test-variant-writepath",
		Name:            "T",
		Version:         "1.0",
		HuggingFaceRepo: "t/r",
		Variants: []CatalogVariant{
			{ID: "fp32", Default: true, Files: fp32Files},
			{ID: "int8-arm", Files: int8Files},
		},
		Files: fp32Files,
	}
	return entry, t.TempDir(), srv.URL
}

func TestModelManager_InstallSelectsNonDefaultVariant(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	require.NoError(t, mm.Install(t.Context(), &entry, "int8-arm", srvURL, nil))

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "int8-arm", got.VariantID, "the selected variant id must be recorded")
	// The selected variant's model file is on disk; the default's is not.
	_, err := os.Stat(filepath.Join(modelsDir, entry.ID, "model_int8.onnx"))
	require.NoError(t, err, "the selected (int8-arm) variant file must be downloaded")
	_, err = os.Stat(filepath.Join(modelsDir, entry.ID, "model.onnx"))
	assert.True(t, os.IsNotExist(err), "the default (fp32) variant file must NOT be downloaded")
}

func TestModelManager_InstallUnknownVariantErrors(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	err := mm.Install(t.Context(), &entry, "does-not-exist", srvURL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown variant")
	assert.False(t, mm.IsInstalled(entry.ID), "a rejected variant must not be recorded as installed")
	assert.Nil(t, mm.GetDownloadState(entry.ID), "a rejected variant must not leave a lingering download state")
}

func TestModelManager_ReinstallRepairsInstalledVariant(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)
	require.NoError(t, mm.Install(t.Context(), &entry, "int8-arm", srvURL, nil))

	int8Path := filepath.Join(modelsDir, entry.ID, "model_int8.onnx")
	require.NoError(t, os.Remove(int8Path))

	require.NoError(t, mm.Reinstall(t.Context(), &entry, srvURL, nil))

	_, err := os.Stat(int8Path)
	require.NoError(t, err, "reinstall must re-fetch the INSTALLED variant's file")
	_, err = os.Stat(filepath.Join(modelsDir, entry.ID, "model.onnx"))
	assert.True(t, os.IsNotExist(err), "reinstall must not fetch the default variant instead")
}

func TestModelManager_ReinstallStaleVariantValidatesBeforeUnload(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	require.NotEmpty(t, entry.RegistryID)

	// Load the model as the primary: UnloadModel refuses the primary, so if the
	// stale-variant check ran AFTER the unload step the error would be "model
	// still in use". Getting the pre-unload "no longer in the catalog" error, with
	// the model still loaded, proves the check runs BEFORE the unload, so a running
	// model is never stranded.
	primaryBN := &BirdNET{ModelInfo: ModelInfo{ID: entry.RegistryID}}
	orch := &Orchestrator{
		ModelInfo: primaryBN.ModelInfo,
		models:    map[string]*modelEntry{entry.RegistryID: {instance: primaryBN}},
		primary:   primaryBN,
	}
	mm := NewModelManager(t.TempDir(), orch, nil)
	// Simulate an install whose variant was later dropped from the catalog.
	mm.installed[entry.ID] = InstalledModel{CatalogID: entry.ID, VariantID: "gone"}

	entryCopy := entry
	err := mm.Reinstall(t.Context(), &entryCopy, "http://unused.invalid", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no longer in the catalog", "must fail the pre-unload variant check, not the post-unload 'model still in use' path")
	assert.True(t, mm.IsInstalled(entry.ID), "a stale-variant reinstall must leave the install in place")
	assert.True(t, orch.IsModelLoaded(entry.RegistryID), "the running model must remain loaded (never unloaded)")
}

func TestModelManager_UninstallStaleVariantDeletesRecordedFile(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	// A model file whose variant id is no longer in the catalog.
	stalePath := filepath.Join(subdir, "perch_v2_gone.onnx")
	require.NoError(t, os.WriteFile(stalePath, []byte("data"), 0o644))

	mm := NewModelManager(modelsDir, nil, nil)
	// Simulate an install whose variant was later dropped from the catalog.
	mm.installed[entry.ID] = InstalledModel{CatalogID: entry.ID, VariantID: "gone", ModelPath: stalePath}

	require.NoError(t, mm.Uninstall(entry.ID))
	assert.False(t, mm.IsInstalled(entry.ID))
	_, err := os.Stat(stalePath)
	assert.True(t, os.IsNotExist(err), "a stale variant's recorded on-disk model file must be deleted, not orphaned")
}

func TestModelManager_DownloadModelFilesRejectsUnknownVariant(t *testing.T) {
	t.Parallel()

	files := []CatalogFile{{RemotePath: "m.onnx", LocalName: "m.onnx", Role: RoleModel, SHA256: "x", SizeBytes: 1}}
	entry := CatalogEntry{
		ID:       "test-backstop",
		Variants: []CatalogVariant{{ID: "fp32", Default: true, Files: files}},
		Files:    files,
	}
	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)
	// Callers register the download before calling downloadModelFiles; mimic that
	// so the backstop's markFailed has a state to update.
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}

	err := mm.downloadModelFiles(t.Context(), &entry, "bogus", "", nil, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown variant")

	state := mm.GetDownloadState(entry.ID)
	require.NotNil(t, state)
	assert.Equal(t, StatusFailed, state.Status, "the backstop must mark the download failed")
	_, statErr := os.Stat(filepath.Join(modelsDir, entry.ID))
	assert.True(t, os.IsNotExist(statErr), "no subdirectory should be created for a rejected variant")
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

	// The permanent BirdNET v2.4 entry is now a real, visible catalog entry whose
	// BuiltIn baseline is always installed. Uninstall must still refuse it: only its
	// variant may be swapped, never removed.
	entry, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok, "birdnet-v2.4 must be present in the catalog")
	require.Equal(t, permanentRegistryID, entry.RegistryID, "birdnet-v2.4 must carry the permanent registry id")

	mm := NewModelManager(t.TempDir(), nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled("birdnet-v2.4"), "the built-in baseline must always be installed")

	err := mm.Uninstall("birdnet-v2.4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot uninstall")
	assert.True(t, mm.IsInstalled("birdnet-v2.4"), "the permanent model must remain installed after a refused uninstall")
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
		if isSharedRole(f.Role) {
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

	// The standalone geomodel entry also gets detected as installed because
	// its shared files exist. Uninstall it first so that shared geomodel
	// files are not retained by the standalone entry.
	if mm.IsInstalled("birdnet-geomodel-v3") {
		require.NoError(t, mm.Uninstall("birdnet-geomodel-v3"))
	}

	require.NoError(t, mm.Uninstall(entry.ID))
	assert.False(t, mm.IsInstalled(entry.ID))

	// Model file should be gone, labels should remain,
	// shared geomodel files should be gone (no other dependent model installed).
	for _, f := range entry.Files {
		var path string
		if isSharedRole(f.Role) {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, err := os.Stat(path)
		switch {
		case f.Role == RoleModel:
			assert.True(t, os.IsNotExist(err), "model file %s should be deleted", f.LocalName)
		case f.Role == RoleLabels:
			require.NoError(t, err, "labels file %s should be retained", f.LocalName)
		case isGeomodelRole(f.Role):
			assert.True(t, os.IsNotExist(err), "geomodel file %s should be deleted when no dependents remain", f.LocalName)
		case f.Role == RoleEmbeddings:
			assert.True(t, os.IsNotExist(err), "embeddings file %s should be deleted when no dependents remain", f.LocalName)
		case f.Role == RoleTaxonomy:
			assert.True(t, os.IsNotExist(err), "taxonomy file %s should be deleted when no dependents remain", f.LocalName)
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
	err := mm.downloadFile(t.Context(), "test-download", srv.URL+"/model.onnx", destPath, checksum, 0)
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
	err := mm.downloadFile(t.Context(), "test-bad-checksum", srv.URL+"/model.onnx", destPath, wrongChecksum, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum")

	// Verify temp files were cleaned up.
	matches, _ := filepath.Glob(destPath + ".*.tmp")
	assert.Empty(t, matches, "temp files should be cleaned up after checksum mismatch")

	// Verify destination file was not created.
	_, err = os.Stat(destPath)
	assert.True(t, os.IsNotExist(err), "destination file should not exist after checksum failure")
}

func TestModelManager_DownloadFile_EmptySHA256(t *testing.T) {
	t.Parallel()

	content := []byte("model data without checksum")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	mm := NewModelManager(t.TempDir(), nil, nil)
	destPath := filepath.Join(mm.modelsDir, "no-checksum", "model.onnx")

	mm.downloading["test-no-checksum"] = &DownloadState{CatalogID: "test-no-checksum", Status: StatusDownloading}
	err := mm.downloadFile(t.Context(), "test-no-checksum", srv.URL+"/model.onnx", destPath, "", 0)
	require.NoError(t, err, "empty expectedSHA256 should skip verification")

	got, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

func TestModelManager_DownloadFile_ContextCancelled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	mm := NewModelManager(t.TempDir(), nil, nil)
	destPath := filepath.Join(mm.modelsDir, "cancelled", "model.onnx")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	mm.downloading["test-cancelled"] = &DownloadState{CatalogID: "test-cancelled", Status: StatusDownloading}
	err := mm.downloadFile(ctx, "test-cancelled", srv.URL+"/model.onnx", destPath, "", 0)
	require.Error(t, err, "cancelled context should produce an error")

	_, statErr := os.Stat(destPath)
	assert.True(t, os.IsNotExist(statErr), "file should not exist after cancelled download")
}

func TestModelManager_Install(t *testing.T) {
	t.Parallel()

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\nspecies_c\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelsONNX:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(modelContent)
		case testPathModelsLabels:
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
	err := mm.Install(t.Context(), &entry, "", srv.URL, progress)
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

	err := mm.Install(t.Context(), &entry, "", "", nil)
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

	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
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

	err := mm.Install(t.Context(), &entry, "", "", nil)
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
		if isSharedRole(f.Role) {
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

	// Remove standalone geomodel entry so shared files are not retained.
	if mm.IsInstalled("birdnet-geomodel-v3") {
		require.NoError(t, mm.Uninstall("birdnet-geomodel-v3"))
	}

	err := mm.Uninstall(entry.ID)
	require.NoError(t, err, "Uninstall must succeed when model is not loaded")
	assert.False(t, mm.IsInstalled(entry.ID), "model must be removed from installed map")

	// Verify per-role file expectations after uninstall.
	for _, f := range entry.Files {
		var path string
		if isSharedRole(f.Role) {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, statErr := os.Stat(path)
		switch {
		case f.Role == RoleModel || f.Role == RoleData:
			assert.True(t, os.IsNotExist(statErr), "%s file %s must be deleted after uninstall", f.Role, f.LocalName)
		case f.Role == RoleLabels:
			require.NoError(t, statErr, "labels file %s must be retained after uninstall", f.LocalName)
		case isGeomodelRole(f.Role):
			assert.True(t, os.IsNotExist(statErr), "geomodel file %s must be deleted when no dependents remain", f.LocalName)
		case f.Role == RoleEmbeddings:
			assert.True(t, os.IsNotExist(statErr), "embeddings file %s must be deleted when no dependents remain", f.LocalName)
		case f.Role == RoleTaxonomy:
			assert.True(t, os.IsNotExist(statErr), "taxonomy file %s must be deleted when no dependents remain", f.LocalName)
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
		if isSharedRole(f.Role) {
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
		ModelInfo: primaryBN.ModelInfo, // mirror the primary, as NewOrchestrator does
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
		if isSharedRole(f.Role) {
			path = filepath.Join(modelsDir, "shared", f.LocalName)
		} else {
			path = filepath.Join(subdir, f.LocalName)
		}
		_, statErr := os.Stat(path)
		assert.NoError(t, statErr, "file %s must still exist after aborted uninstall", f.LocalName)
	}
}

// TestModelManager_UninstallDeregistersWhenFileDeletionFails covers the
// best-effort uninstall behavior: when a model file cannot be removed (here
// simulated by replacing it with a non-empty directory so os.Remove returns a
// non-ENOENT error), Uninstall must NOT abort early. It still de-registers the
// model from the installed map and clears config, then surfaces the deletion
// failure as an error. Before this change, the first failed os.Remove returned
// immediately, leaving the model registered.
func TestModelManager_UninstallDeregistersWhenFileDeletionFails(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.RegistryID, "perch-v2 must have a RegistryID for this test")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	for _, f := range entry.Files {
		var dir string
		if isSharedRole(f.Role) {
			dir = filepath.Join(modelsDir, "shared")
		} else {
			dir = subdir
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	// Empty models map: IsModelLoaded returns false, so unload is skipped and
	// Uninstall proceeds to delete files.
	orch := &Orchestrator{
		models: make(map[string]*modelEntry),
	}

	mm := NewModelManager(modelsDir, orch, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID), "model must be installed before uninstall")

	// Remove the standalone geomodel entry so shared files are not retained,
	// mirroring TestModelManager_UninstallSucceedsWhenModelNotLoaded.
	if mm.IsInstalled("birdnet-geomodel-v3") {
		require.NoError(t, mm.Uninstall("birdnet-geomodel-v3"))
	}

	// Sabotage one model/data file so os.Remove fails with a non-ENOENT error:
	// replace the file with a non-empty directory (os.Remove returns ENOTEMPTY).
	var sabotaged string
	for _, f := range entry.Files {
		if f.Role != RoleModel && f.Role != RoleData {
			continue
		}
		path := filepath.Join(subdir, f.LocalName)
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.MkdirAll(path, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o644))
		sabotaged = path
		break
	}
	require.NotEmpty(t, sabotaged, "perch-v2 must have a model or data file to sabotage")

	err := mm.Uninstall(entry.ID)
	require.Error(t, err, "Uninstall must surface the file-deletion failure")
	assert.Contains(t, err.Error(), "failed to remove some files")
	assert.False(t, mm.IsInstalled(entry.ID),
		"model must be de-registered even when some files could not be deleted")
}

// TestModelManager_ReinstallRefusesLoadedPrimary documents and guards the
// behavior of the new pre-overwrite unload step in Reinstall: when the target
// is the loaded primary model (which UnloadModel refuses to unload), Reinstall
// aborts with "model still in use" before touching any files, and the model
// stays installed. This is a deliberate behavior change from the prior code,
// which overwrote files in place even while the primary was loaded.
func TestModelManager_ReinstallRefusesLoadedPrimary(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.RegistryID, "perch-v2 must have a RegistryID for this test")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	for _, f := range entry.Files {
		var dir string
		if isSharedRole(f.Role) {
			dir = filepath.Join(modelsDir, "shared")
		} else {
			dir = subdir
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	// Model loaded AND set as primary: UnloadModel refuses the primary, so the
	// new pre-overwrite guard must abort the reinstall.
	primaryBN := &BirdNET{ModelInfo: ModelInfo{ID: entry.RegistryID}}
	orch := &Orchestrator{
		ModelInfo: primaryBN.ModelInfo, // mirror the primary, as NewOrchestrator does
		models: map[string]*modelEntry{
			entry.RegistryID: {instance: primaryBN},
		},
		primary: primaryBN,
	}

	mm := NewModelManager(modelsDir, orch, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID), "model must be installed before reinstall attempt")

	entryCopy := entry
	// baseURL is never reached: the unload guard aborts before any download.
	err := mm.Reinstall(t.Context(), &entryCopy, "http://unused.invalid", nil)
	require.Error(t, err, "Reinstall must abort when the loaded primary cannot be unloaded")
	assert.Contains(t, err.Error(), "model still in use")
	assert.True(t, mm.IsInstalled(entry.ID), "model must remain installed after a refused reinstall")
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
			{RemotePath: "geomodel.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodelModel, SHA256: geomodelChecksum, SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodelLabels, SHA256: geomodelLabelsChecksum, SizeBytes: int64(len(geomodelLabelsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
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
			{RemotePath: "geomodel.onnx", LocalName: "geomodel.onnx", Role: RoleGeomodelModel, SHA256: geomodelChecksum, SizeBytes: int64(len(geomodelContent))},
		},
	}

	mm := NewModelManager(modelsDir, nil, nil)
	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
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
			if isSharedRole(f.Role) {
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
		if isGeomodelRole(f.Role) {
			path := filepath.Join(sharedDir, f.LocalName)
			_, err := os.Stat(path)
			require.NoError(t, err, "geomodel file %s must be retained while birdnet-v3.0 is installed", f.LocalName)
		}
	}

	// Now uninstall birdnet-v3.0 and the standalone geomodel entry.
	require.NoError(t, mm.Uninstall("birdnet-v3.0"))
	if mm.IsInstalled("birdnet-geomodel-v3") {
		require.NoError(t, mm.Uninstall("birdnet-geomodel-v3"))
	}

	// Shared geomodel files should now be deleted.
	for _, f := range entryV3.Files {
		if isGeomodelRole(f.Role) {
			path := filepath.Join(sharedDir, f.LocalName)
			_, err := os.Stat(path)
			assert.True(t, os.IsNotExist(err), "geomodel file %s must be deleted when no dependents remain", f.LocalName)
		}
	}
}

func TestModelManager_Uninstall_SharedFilesRetainedWhileDownloading(t *testing.T) {
	t.Parallel()

	entryPerch, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	subdir := filepath.Join(modelsDir, entryPerch.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	for _, f := range entryPerch.Files {
		var dir string
		if isSharedRole(f.Role) {
			dir = sharedDir
		} else {
			dir = subdir
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.LocalName), []byte("data"), 0o644))
	}

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled("perch-v2"))

	// Simulate another geomodel-dependent model downloading concurrently.
	mm.mu.Lock()
	mm.downloading["birdnet-v3.0"] = &DownloadState{CatalogID: "birdnet-v3.0", Status: StatusDownloading}
	mm.mu.Unlock()

	require.NoError(t, mm.Uninstall("perch-v2"))

	// Shared geomodel files must be retained because birdnet-v3.0 is downloading.
	for _, f := range entryPerch.Files {
		if isGeomodelRole(f.Role) {
			path := filepath.Join(sharedDir, f.LocalName)
			_, err := os.Stat(path)
			require.NoError(t, err, "geomodel file %s must be retained while another dependent model is downloading", f.LocalName)
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
			{RemotePath: "companion.bin", LocalName: "companion.bin", Role: RoleGeomodelModel, HuggingFaceRepo: "companion-repo"},
		},
	}

	// Verify the URL construction logic: when HuggingFaceRepo is set on a
	// CatalogFile, Install should use it instead of the entry-level repo.
	for _, f := range entry.Files {
		repo := entry.HuggingFaceRepo
		if f.HuggingFaceRepo != "" {
			repo = f.HuggingFaceRepo
		}
		got := buildHuggingFaceURL(conf.DefaultHuggingFaceEndpoint, repo, f.RemotePath)
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
			{RemotePath: "geomodel.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodelModel, SHA256: sha256Hex(geomodelContent), SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodelLabels, SHA256: sha256Hex(geomodelLabelsContent), SizeBytes: int64(len(geomodelLabelsContent))},
		},
		GeomodelVersion: "v3",
	}

	// Save original settings to restore after test.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)

	// Verify range filter config was set.
	current := conf.GetSettings()
	assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model)
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geomodel_v3.onnx"), current.BirdNET.RangeFilter.ModelPath)
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geomodel_v3_labels.txt"), current.BirdNET.RangeFilter.LabelsPath)
}

func TestModelManager_Uninstall_GeomodelConfigClearing(t *testing.T) {
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
		ID:              "test-geo-uninstall-config",
		Name:            "Uninstall Config Test",
		Version:         "1.0",
		RegistryID:      RegistryIDPerchV2,
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: sha256Hex(modelContent), SizeBytes: int64(len(modelContent))},
			{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labelsContent), SizeBytes: int64(len(labelsContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodelModel, SHA256: sha256Hex(geomodelContent), SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodelLabels, SHA256: sha256Hex(geomodelLabelsContent), SizeBytes: int64(len(geomodelLabelsContent))},
		},
		GeomodelVersion: "v3",
	}

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	// Install to set config.
	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)

	current := conf.GetSettings()
	require.Equal(t, "v3", current.BirdNET.RangeFilter.Model, "precondition: install must set model to v3")

	// Make the entry resolvable by Uninstall via the active catalog. Setting
	// activeCatalog (under its lock) instead of mutating the shared
	// EmbeddedCatalog global keeps the lock-guarded catalog accessors race-free.
	withEntry := make([]CatalogEntry, len(EmbeddedCatalog), len(EmbeddedCatalog)+1)
	copy(withEntry, EmbeddedCatalog)
	withEntry = append(withEntry, entry)
	setActiveCatalog(withEntry)
	t.Cleanup(func() { setActiveCatalog(nil) })

	// Uninstall should clear the range filter config.
	require.NoError(t, mm.Uninstall("test-geo-uninstall-config"))

	current = conf.GetSettings()
	assert.Empty(t, current.BirdNET.RangeFilter.Model, "uninstall must clear range filter model")
	assert.Empty(t, current.BirdNET.RangeFilter.ModelPath, "uninstall must clear range filter model path")
	assert.Empty(t, current.BirdNET.RangeFilter.LabelsPath, "uninstall must clear range filter labels path")
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
			name: "has geomodel model file",
			entry: CatalogEntry{Files: []CatalogFile{
				{Role: RoleModel},
				{Role: RoleGeomodelModel},
			}},
			want: true,
		},
		{
			name: "has geomodel labels file",
			entry: CatalogEntry{Files: []CatalogFile{
				{Role: RoleModel},
				{Role: RoleGeomodelLabels},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, HasGeomodelFiles(&tt.entry))
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

			assert.True(t, HasGeomodelFiles(&entry), "%s should have geomodel files", id)

			var geoFileCount int
			for _, f := range entry.Files {
				if isGeomodelRole(f.Role) {
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

func TestVerifySHA256(t *testing.T) {
	t.Parallel()

	content := []byte("test file content for SHA256 verification")
	expectedHash := sha256Hex(content)

	t.Run("valid file matches", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "valid.bin")
		require.NoError(t, os.WriteFile(path, content, 0o644))
		assert.True(t, verifySHA256(path, expectedHash))
	})

	t.Run("corrupt file mismatches", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "corrupt.bin")
		require.NoError(t, os.WriteFile(path, []byte("wrong content"), 0o644))
		assert.False(t, verifySHA256(path, expectedHash))
	})

	t.Run("missing file returns false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, verifySHA256(filepath.Join(t.TempDir(), "missing.bin"), expectedHash))
	})

	t.Run("empty expected hash skips validation", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "any.bin")
		require.NoError(t, os.WriteFile(path, []byte("anything"), 0o644))
		assert.True(t, verifySHA256(path, ""))
	})
}

func TestModelManager_Install_RedownloadsCorruptSharedFile(t *testing.T) {
	t.Parallel()

	modelContent := []byte("model-data")
	geomodelContent := []byte("correct-geomodel-data")
	modelChecksum := sha256Hex(modelContent)
	geomodelChecksum := sha256Hex(geomodelContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelONNX:
			_, _ = w.Write(modelContent)
		case testPathGeomodel:
			_, _ = w.Write(geomodelContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	modelsDir := t.TempDir()

	// Pre-create a corrupt shared file.
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	corruptPath := filepath.Join(sharedDir, "geomodel.onnx")
	require.NoError(t, os.WriteFile(corruptPath, []byte("corrupt-data"), 0o644))

	entry := CatalogEntry{
		ID:              "test-redownload-corrupt",
		Name:            "Redownload Corrupt Test",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geomodel.onnx", Role: RoleGeomodelModel, SHA256: geomodelChecksum, SizeBytes: int64(len(geomodelContent))},
		},
	}

	mm := NewModelManager(modelsDir, nil, nil)
	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)

	// Verify the corrupt file was replaced with correct content.
	got, err := os.ReadFile(corruptPath)
	require.NoError(t, err)
	assert.Equal(t, geomodelContent, got, "corrupt shared file should be re-downloaded")
}

func TestModelManager_Install_GeomodelVersionWiring(t *testing.T) {
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
		ID:              "test-geomodel-version",
		Name:            "Geomodel Version Test",
		Version:         "1.0",
		GeomodelVersion: "v3",
		RegistryID:      RegistryIDPerchV2,
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "model.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: sha256Hex(modelContent), SizeBytes: int64(len(modelContent))},
			{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labelsContent), SizeBytes: int64(len(labelsContent))},
			{RemotePath: "geomodel.onnx", LocalName: "geo_v3.onnx", Role: RoleGeomodelModel, SHA256: sha256Hex(geomodelContent), SizeBytes: int64(len(geomodelContent))},
			{RemotePath: "geomodel_labels.txt", LocalName: "geo_v3_labels.txt", Role: RoleGeomodelLabels, SHA256: sha256Hex(geomodelLabelsContent), SizeBytes: int64(len(geomodelLabelsContent))},
		},
	}

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)

	current := conf.GetSettings()
	assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model, "geomodel version from catalog should be used")
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geo_v3.onnx"), current.BirdNET.RangeFilter.ModelPath)
	assert.Equal(t, filepath.Join(modelsDir, "shared", "geo_v3_labels.txt"), current.BirdNET.RangeFilter.LabelsPath)
}

func TestIsGeomodelRole(t *testing.T) {
	t.Parallel()

	assert.True(t, isGeomodelRole(RoleGeomodelModel))
	assert.True(t, isGeomodelRole(RoleGeomodelLabels))
	assert.False(t, isGeomodelRole(RoleModel))
	assert.False(t, isGeomodelRole(RoleLabels))
	assert.False(t, isGeomodelRole(RoleEmbeddings))
	assert.False(t, isGeomodelRole(RoleData))
	assert.False(t, isGeomodelRole(""))
}

func TestBuildHuggingFaceURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		repo     string
		filePath string
		want     string
	}{
		{
			name:     "simple file",
			endpoint: conf.DefaultHuggingFaceEndpoint,
			repo:     "tphakala/BirdNET-v3.0",
			filePath: "birdnet_v3.0.onnx",
			want:     "https://huggingface.co/tphakala/BirdNET-v3.0/resolve/main/birdnet_v3.0.onnx",
		},
		{
			name:     "nested path",
			endpoint: conf.DefaultHuggingFaceEndpoint,
			repo:     "tphakala/BattyBirdNET-onnx",
			filePath: "fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
			want:     "https://huggingface.co/tphakala/BattyBirdNET-onnx/resolve/main/fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
		},
		{
			name:     "mirror host",
			endpoint: "https://hf-mirror.com",
			repo:     "tphakala/BirdNET-v3.0",
			filePath: "birdnet_v3.0.onnx",
			want:     "https://hf-mirror.com/tphakala/BirdNET-v3.0/resolve/main/birdnet_v3.0.onnx",
		},
		{
			name:     "mirror host with a path prefix",
			endpoint: "https://mirror.example.com/hf",
			repo:     "tphakala/BattyBirdNET-onnx",
			filePath: "fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
			want:     "https://mirror.example.com/hf/tphakala/BattyBirdNET-onnx/resolve/main/fp32/BattyBirdNET-EU-256kHz_fp32.onnx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildHuggingFaceURL(tt.endpoint, tt.repo, tt.filePath)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestModelManager_HuggingFaceEndpoint verifies the resolution the download
// path uses: the settings field when config sync is enabled, HF_ENDPOINT
// otherwise, and the default host when neither is set.
func TestModelManager_HuggingFaceEndpoint(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings and the
	// HF_ENDPOINT environment variable.

	// Save original settings to restore after test. Cloned, not captured by
	// pointer: conf.GetSettings returns the live snapshot, so restoring the same
	// pointer would restore any in-place mutation along with it.
	origSettings := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	// newManager publishes settings with the given endpoint override and
	// returns a manager wired to them, mirroring production where the manager
	// holds the published settings pointer. It also pins HF_ENDPOINT, which
	// would otherwise be inherited from the environment running the test; the
	// people most likely to have it exported are the mirror users this feature
	// exists for, so an unpinned test would fail for exactly them.
	newManager := func(t *testing.T, configured, env string) *ModelManager {
		t.Helper()
		t.Setenv(conf.HuggingFaceEndpointEnvVar, env)
		settings := conftest.GetTestSettings()
		settings.BirdNET.HuggingFaceEndpoint = configured
		conf.StoreSettings(settings)
		return NewModelManager(t.TempDir(), nil, settings)
	}

	t.Run("default when no override is configured", func(t *testing.T) {
		mm := newManager(t, "", "")
		assert.Equal(t, conf.DefaultHuggingFaceEndpoint, mm.huggingFaceEndpoint())
	})

	t.Run("settings field is honoured", func(t *testing.T) {
		mm := newManager(t, "https://hf-mirror.com/", "")
		assert.Equal(t, "https://hf-mirror.com", mm.huggingFaceEndpoint())
	})

	t.Run("settings change takes effect without recreating the manager", func(t *testing.T) {
		mm := newManager(t, "", "")
		require.Equal(t, conf.DefaultHuggingFaceEndpoint, mm.huggingFaceEndpoint())

		updated := conf.CloneSettings(conf.GetSettings())
		updated.BirdNET.HuggingFaceEndpoint = "https://hf-mirror.com"
		conf.StoreSettings(updated)

		assert.Equal(t, "https://hf-mirror.com", mm.huggingFaceEndpoint(),
			"endpoint must be read fresh so a settings change hot-reloads")
	})

	t.Run("env var is honoured when the settings field is empty", func(t *testing.T) {
		mm := newManager(t, "", "https://hf-mirror.com")
		assert.Equal(t, "https://hf-mirror.com", mm.huggingFaceEndpoint())
	})

	t.Run("settings field wins over the env var", func(t *testing.T) {
		mm := newManager(t, "https://settings-mirror.example.com", "https://hf-mirror.com")
		assert.Equal(t, "https://settings-mirror.example.com", mm.huggingFaceEndpoint())
	})

	t.Run("published settings are read when none were injected", func(t *testing.T) {
		t.Setenv(conf.HuggingFaceEndpointEnvVar, "")
		settings := conftest.GetTestSettings()
		settings.BirdNET.HuggingFaceEndpoint = "https://hf-mirror.com"
		conf.StoreSettings(settings)

		mm := NewModelManager(t.TempDir(), nil, nil)
		assert.Equal(t, "https://hf-mirror.com", mm.huggingFaceEndpoint(),
			"conf.CurrentOrFallback must prefer the published snapshot")
	})

	t.Run("malformed settings field falls back to the default", func(t *testing.T) {
		mm := newManager(t, "hf-mirror.com", "")
		assert.Equal(t, conf.DefaultHuggingFaceEndpoint, mm.huggingFaceEndpoint())
	})

	t.Run("credential-bearing settings field falls back to the default", func(t *testing.T) {
		mm := newManager(t, "https://user:hunter2@hf-mirror.com", "")
		assert.Equal(t, conf.DefaultHuggingFaceEndpoint, mm.huggingFaceEndpoint(),
			"credentials must never reach a download URL, a log line, or a support dump")
	})
}

// TestModelManager_Install_UsesConfiguredEndpoint drives a real install through
// the repo-construction path (baseURL empty) with the endpoint override
// pointing at a local server, proving the mirror host reaches every file
// download rather than only the URL helper.
func TestModelManager_Install_UsesConfiguredEndpoint(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\n")

	var requested []string
	var requestedMu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMu.Lock()
		requested = append(requested, r.URL.Path)
		requestedMu.Unlock()

		switch r.URL.Path {
		case "/mirror/test/repo/resolve/main/models/test.onnx":
			_, _ = w.Write(modelContent)
		case "/mirror/companion/repo/resolve/main/models/labels.txt":
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-mirror-install",
		Name:            "Mirror Test Model",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: sha256Hex(modelContent), SizeBytes: int64(len(modelContent))},
			{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labelsContent), SizeBytes: int64(len(labelsContent)), HuggingFaceRepo: "companion/repo"},
		},
	}

	// Cloned rather than captured by pointer, so an in-place mutation cannot be
	// restored along with the snapshot.
	origSettings := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	t.Setenv(conf.HuggingFaceEndpointEnvVar, "")
	settings := conftest.GetTestSettings()
	// Trailing slash included on purpose: normalization must not produce "//".
	settings.BirdNET.HuggingFaceEndpoint = srv.URL + "/mirror/"
	conf.StoreSettings(settings)

	modelsDir := t.TempDir()
	// Injected settings are deliberately nil: a non-nil value makes a successful
	// install run applyConfigForInstall, which calls conf.SaveSettings and writes
	// the developer's real ~/.config/birdnet-go/config.yaml. The endpoint still
	// resolves from the published snapshot above via conf.CurrentOrFallback, so
	// the download path under test is unaffected.
	//
	// The trade, stated so it is not rediscovered as a surprise: production always
	// passes non-nil settings, so this test does not cover the install-then-persist
	// step. That step is covered by the geomodel install tests, which need non-nil
	// settings to assert on what gets persisted, and which write the real config
	// for the same reason. Fixing that properly needs a config-path seam.
	mm := NewModelManager(modelsDir, nil, nil)

	// baseURL is empty so the download path builds URLs from the endpoint and
	// the repo, which is what a real install does.
	require.NoError(t, mm.Install(t.Context(), &entry, "", "", nil))

	assert.True(t, mm.IsInstalled("test-mirror-install"))

	gotModel, err := os.ReadFile(filepath.Join(modelsDir, "test-mirror-install", "test.onnx"))
	require.NoError(t, err)
	assert.Equal(t, modelContent, gotModel)

	requestedMu.Lock()
	defer requestedMu.Unlock()
	assert.Equal(t, []string{
		"/mirror/test/repo/resolve/main/models/test.onnx",
		"/mirror/companion/repo/resolve/main/models/labels.txt",
	}, requested, "both the entry repo and the per-file repo must be fetched from the mirror")
}

func TestModelManager_Reinstall(t *testing.T) {
	t.Parallel()

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\nspecies_c\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testPathModelsONNX:
			_, _ = w.Write(modelContent)
		case testPathModelsLabels:
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-reinstall-model",
		Name:            "Test Reinstall",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: labelsChecksum, SizeBytes: int64(len(labelsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	// Install first.
	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)
	require.True(t, mm.IsInstalled("test-reinstall-model"))

	// Delete the model file to simulate corruption/missing file.
	modelPath := filepath.Join(modelsDir, "test-reinstall-model", "test.onnx")
	require.NoError(t, os.Remove(modelPath))
	_, err = os.Stat(modelPath)
	require.True(t, os.IsNotExist(err), "model file must be deleted before reinstall")

	// Reinstall should re-download the missing file.
	progress := make(chan DownloadState, 100)
	err = mm.Reinstall(t.Context(), &entry, srv.URL, progress)
	require.NoError(t, err)

	// Verify the model file was re-downloaded with correct content.
	gotModel, err := os.ReadFile(modelPath)
	require.NoError(t, err)
	assert.Equal(t, modelContent, gotModel)

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

func TestModelManager_Reinstall_NotInstalled(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)

	entry := CatalogEntry{
		ID:   "test-not-installed",
		Name: "Not Installed Model",
	}

	err := mm.Reinstall(t.Context(), &entry, "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed")
}

func TestModelManager_Reinstall_SkipsValidFiles(t *testing.T) {
	t.Parallel()

	modelContent := []byte("fake-onnx-model-binary-data")
	labelsContent := []byte("species_a\nspecies_b\nspecies_c\n")
	modelChecksum := sha256Hex(modelContent)
	labelsChecksum := sha256Hex(labelsContent)

	var downloadCount atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadCount.Add(1)
		switch r.URL.Path {
		case testPathModelsONNX:
			_, _ = w.Write(modelContent)
		case testPathModelsLabels:
			_, _ = w.Write(labelsContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	entry := CatalogEntry{
		ID:              "test-reinstall-skip",
		Name:            "Test Reinstall Skip",
		Version:         "1.0",
		HuggingFaceRepo: "test/repo",
		Files: []CatalogFile{
			{RemotePath: "models/test.onnx", LocalName: "test.onnx", Role: RoleModel, SHA256: modelChecksum, SizeBytes: int64(len(modelContent))},
			{RemotePath: "models/labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: labelsChecksum, SizeBytes: int64(len(labelsContent))},
		},
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)

	// Install first.
	err := mm.Install(t.Context(), &entry, "", srv.URL, nil)
	require.NoError(t, err)
	require.True(t, mm.IsInstalled("test-reinstall-skip"))

	// Reset the download counter after the initial install.
	downloadCount.Store(0)

	// Reinstall without deleting anything; all files should pass SHA256 validation.
	err = mm.Reinstall(t.Context(), &entry, srv.URL, nil)
	require.NoError(t, err)

	// No HTTP requests should have been made since all files are valid.
	assert.Equal(t, int64(0), downloadCount.Load(), "expected zero downloads when all files are valid")
}

// TestModelManager_TopologyChangedCallback verifies that a registered callback
// fires when the topology-changed notify path runs, and that the notify path is
// a safe no-op when no callback is set.
func TestModelManager_TopologyChangedCallback(t *testing.T) {
	t.Parallel()

	mm := NewModelManager(t.TempDir(), nil, nil)

	// No callback set: notify must be a safe no-op.
	assert.NotPanics(t, mm.notifyTopologyChanged)

	var fired atomic.Int64
	mm.SetTopologyChangedCallback(func() {
		fired.Add(1)
	})

	mm.notifyTopologyChanged()
	assert.Equal(t, int64(1), fired.Load(), "callback should fire once per notify")

	mm.notifyTopologyChanged()
	assert.Equal(t, int64(2), fired.Load(), "callback should fire on every notify")

	// Clearing the callback disables it without panicking.
	mm.SetTopologyChangedCallback(nil)
	assert.NotPanics(t, mm.notifyTopologyChanged)
	assert.Equal(t, int64(2), fired.Load(), "cleared callback must not fire")
}
