package classifier

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestModelManager_InstallOrReplace_InstallsWhenAbsent verifies that
// InstallOrReplace performs a fresh install (default variant) when nothing is
// installed for the entry.
func TestModelManager_InstallOrReplace_InstallsWhenAbsent(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "fp32", got.VariantID, "an absent variant selection installs the default")
	assert.FileExists(t, filepath.Join(modelsDir, entry.ID, "model.onnx"))
}

// TestModelManager_InstallOrReplace_SwitchesVariant verifies the
// download-before-delete switch: after switching fp32 -> int8-arm, the new
// variant is recorded and on disk, the superseded model file is removed, and the
// shared companion (labels) is retained.
func TestModelManager_InstallOrReplace_SwitchesVariant(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	// Install the default (fp32) first.
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))
	require.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID)

	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	int8Path := filepath.Join(modelsDir, entry.ID, "model_int8.onnx")
	labelsPath := filepath.Join(modelsDir, entry.ID, "labels.txt")
	require.FileExists(t, fp32Path)

	// Switch to int8-arm.
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "int8-arm", srvURL, nil))

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "int8-arm", got.VariantID, "the switch must record the new variant")
	assert.FileExists(t, int8Path, "the new variant's model file must be on disk")
	_, err := os.Stat(fp32Path)
	assert.True(t, os.IsNotExist(err), "the superseded variant's model file must be removed")
	assert.FileExists(t, labelsPath, "the shared labels companion must be retained across the switch")
	assert.Nil(t, mm.GetDownloadState(entry.ID), "no download state may linger after a completed switch")
}

// TestModelManager_InstallOrReplace_SameVariantIsNoOp verifies that requesting
// the already-installed variant is an idempotent no-op.
func TestModelManager_InstallOrReplace_SameVariantIsNoOp(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)

	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))
	require.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID)

	// Re-selecting the installed variant (by explicit id and by default) is a no-op.
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "fp32", srvURL, nil))
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))

	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID)
	assert.Nil(t, mm.GetDownloadState(entry.ID))
}

// TestModelManager_InstallOrReplace_FailedSwitchKeepsOldVariant verifies
// download-before-delete: when the new variant's download fails, the old variant
// stays installed and its files remain intact.
func TestModelManager_InstallOrReplace_FailedSwitchKeepsOldVariant(t *testing.T) {
	t.Parallel()

	fp32Data, labels := []byte("fp32-model"), []byte("a\nb\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fp32.onnx":
			_, _ = w.Write(fp32Data)
		case "/labels.txt":
			_, _ = w.Write(labels)
		default:
			http.NotFound(w, r) // int8.onnx is intentionally not served
		}
	}))
	t.Cleanup(srv.Close)

	fp32Files := []CatalogFile{
		{RemotePath: "fp32.onnx", LocalName: "model.onnx", Role: RoleModel, SHA256: sha256Hex(fp32Data), SizeBytes: int64(len(fp32Data))},
		{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labels), SizeBytes: int64(len(labels))},
	}
	int8Data := []byte("int8-model")
	int8Files := []CatalogFile{
		{RemotePath: "int8.onnx", LocalName: "model_int8.onnx", Role: RoleModel, SHA256: sha256Hex(int8Data), SizeBytes: int64(len(int8Data))},
		{RemotePath: "labels.txt", LocalName: "labels.txt", Role: RoleLabels, SHA256: sha256Hex(labels), SizeBytes: int64(len(labels))},
	}
	entry := CatalogEntry{
		ID:              "test-replace-fail",
		Name:            "T",
		Version:         "1.0",
		HuggingFaceRepo: "t/r",
		Variants: []CatalogVariant{
			{ID: "fp32", Default: true, Files: fp32Files},
			{ID: "int8-arm", Files: int8Files},
		},
		Files: fp32Files,
	}

	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, nil)
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srv.URL, nil))
	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	require.FileExists(t, fp32Path)

	// The switch to int8-arm fails because int8.onnx 404s.
	err := mm.InstallOrReplace(t.Context(), &entry, "int8-arm", srv.URL, nil)
	require.Error(t, err)

	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID,
		"a failed switch must leave the old variant installed")
	assert.FileExists(t, fp32Path, "a failed switch must leave the old variant's file intact")
}

// TestModelManager_UninstallRejectedWhileDownloading verifies that Uninstall
// refuses while a download/switch is in flight, so a concurrent switch cannot
// resurrect a zombie install.
func TestModelManager_UninstallRejectedWhileDownloading(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	writeVariantModelFile(t, modelsDir, &entry, "fp32")

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(entry.ID))

	// Simulate an in-flight download/switch.
	mm.mu.Lock()
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}
	mm.mu.Unlock()

	err := mm.Uninstall(entry.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "being downloaded")
	assert.True(t, mm.IsInstalled(entry.ID), "the model must remain installed when uninstall is refused")
}

// TestModelManager_ScanInstalledPreservesInFlight verifies that a scan does not
// drop an in-flight install/switch (present in mm.downloading) even when its
// files are not yet on disk, so a concurrent switch is not corrupted.
func TestModelManager_ScanInstalledPreservesInFlight(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	mm := NewModelManager(t.TempDir(), nil, nil)

	// Simulate the mid-switch state: a new variant recorded and marked downloading
	// with no files on disk yet.
	mm.mu.Lock()
	mm.installed[entry.ID] = InstalledModel{CatalogID: entry.ID, VariantID: "int8-arm"}
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}
	mm.mu.Unlock()

	mm.ScanInstalled()

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "int8-arm", got.VariantID, "an in-flight switch must survive a concurrent scan")
}

// TestScanVariantEntry_SettingsHintBreaksTie verifies that when both variant
// model files are present on disk (a crashed-replace remnant), the settings hint
// resolves to the variant the loader will actually open, not merely the default.
func TestScanVariantEntry_SettingsHintBreaksTie(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	writeVariantModelFile(t, modelsDir, &entry, "fp32")
	writeVariantModelFile(t, modelsDir, &entry, "int8-arm")

	int8Files, ok := variantFilesByID(&entry, "int8-arm")
	require.True(t, ok)
	int8Model, _ := modelAndLabelsFiles(int8Files)
	require.NotEmpty(t, int8Model)

	// With the hint, the non-default variant wins the tie.
	im, ok := scanVariantEntry(&entry, subdir, int8Model)
	require.True(t, ok)
	assert.Equal(t, "int8-arm", im.VariantID)

	// Without a hint, resolution falls back to the default variant.
	imDefault, ok := scanVariantEntry(&entry, subdir, "")
	require.True(t, ok)
	assert.Equal(t, "fp32", imDefault.VariantID)
}

// TestRemoveSupersededVariantFiles_SkipsSharedRoles verifies that superseded-file
// cleanup deletes the old variant's non-shared model file but never raw-deletes a
// shared-role companion, even one the new variant drops.
func TestRemoveSupersededVariantFiles_SkipsSharedRoles(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	entry := CatalogEntry{
		ID:      "test-superseded",
		Version: "1.0",
		Variants: []CatalogVariant{
			{ID: "old", Default: true, Files: []CatalogFile{
				{LocalName: "old_model.onnx", Role: RoleModel},
				{LocalName: "shared_taxonomy.csv", Role: RoleTaxonomy}, // shared role, dropped by "new"
			}},
			{ID: "new", Files: []CatalogFile{
				{LocalName: "new_model.onnx", Role: RoleModel},
			}},
		},
	}

	subdir := filepath.Join(modelsDir, entry.ID)
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	oldModel := filepath.Join(subdir, "old_model.onnx")
	sharedTaxonomy := filepath.Join(sharedDir, "shared_taxonomy.csv")
	require.NoError(t, os.WriteFile(oldModel, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(sharedTaxonomy, []byte("t"), 0o600))

	mm := NewModelManager(modelsDir, nil, nil)
	mm.removeSupersededVariantFiles(GetLogger(), &entry, "old", "new")

	_, err := os.Stat(oldModel)
	assert.True(t, os.IsNotExist(err), "the superseded non-shared model file must be removed")
	assert.FileExists(t, sharedTaxonomy, "a shared-role file must never be raw-deleted by superseded cleanup")
}

// TestOrchestrator_ResolveInstalledPaths_NonDefaultVariant verifies that the
// orchestrator fallback resolves a non-default variant that is on disk, rather
// than probing only the resolved default variant's filename.
func TestOrchestrator_ResolveInstalledPaths_NonDefaultVariant(t *testing.T) {
	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	int8ModelPath := writeVariantModelFile(t, modelsDir, &entry, "int8-arm")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	modelPath, _, _ := o.resolveInstalledPaths(RegistryIDPerchV2)
	assert.Equal(t, int8ModelPath, modelPath,
		"resolveInstalledPaths must find the installed non-default variant on disk")
}

// TestModelManager_InstallOrReplace_RollsBackOnLoadFailure verifies that when the
// new variant is downloaded and swapped in but fails to LOAD, the switch rolls
// back to the previous variant and reports failure rather than reporting success
// with no classifier loaded. BSG is a known registry id with no loader, so
// LoadModel fails deterministically after the unload.
func TestModelManager_InstallOrReplace_RollsBackOnLoadFailure(t *testing.T) {
	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	entry.RegistryID = RegistryIDBSG

	// Fake orchestrator with the model "loaded" (non-primary, nil instance so the
	// unload closes nothing). LoadModel(BSG) has no registered loader, so the switch
	// cannot re-load after the unload -> rollback.
	orch := &Orchestrator{models: map[string]*modelEntry{entry.RegistryID: {}}}
	orch.SetModelsDir(modelsDir)
	mm := NewModelManager(modelsDir, orch, nil)

	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))
	require.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID)
	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	int8Path := filepath.Join(modelsDir, entry.ID, "model_int8.onnx")
	require.FileExists(t, fp32Path)

	err := mm.InstallOrReplace(t.Context(), &entry, "int8-arm", srvURL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load")

	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID,
		"a load failure must roll back to the previous variant")
	assert.FileExists(t, fp32Path, "the previous variant's file must be kept on rollback")
	_, statErr := os.Stat(int8Path)
	assert.True(t, os.IsNotExist(statErr), "the failed new variant's file must be removed on rollback")
}

// TestModelManager_InstallOrReplace_UnloadFailureKeepsOldAndCleansNew verifies
// that when the old model cannot be unloaded (it is the primary), the switch
// aborts with the old variant intact and the freshly downloaded new files removed.
func TestModelManager_InstallOrReplace_UnloadFailureKeepsOldAndCleansNew(t *testing.T) {
	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	entry.RegistryID = RegistryIDBSG

	// Primary models are refused by UnloadModel, forcing the unload-failure path.
	primary := &BirdNET{ModelInfo: ModelInfo{ID: entry.RegistryID}}
	orch := &Orchestrator{
		ModelInfo: primary.ModelInfo,
		models:    map[string]*modelEntry{entry.RegistryID: {instance: primary}},
		primary:   primary,
	}
	orch.SetModelsDir(modelsDir)
	mm := NewModelManager(modelsDir, orch, nil)

	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))
	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	int8Path := filepath.Join(modelsDir, entry.ID, "model_int8.onnx")
	require.FileExists(t, fp32Path)

	err := mm.InstallOrReplace(t.Context(), &entry, "int8-arm", srvURL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "still in use")

	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID,
		"an unload failure must leave the old variant installed")
	assert.FileExists(t, fp32Path, "the old variant's file must be intact")
	_, statErr := os.Stat(int8Path)
	assert.True(t, os.IsNotExist(statErr), "the aborted switch's new file must be cleaned up")
}

// TestModelManager_InstallOrReplace_RejectsWhileDownloading verifies the
// in-flight guard: a switch is refused while a download is already registered.
func TestModelManager_InstallOrReplace_RejectsWhileDownloading(t *testing.T) {
	t.Parallel()

	entry, modelsDir, srvURL := twoVariantServerEntry(t)
	mm := NewModelManager(modelsDir, nil, nil)
	mm.mu.Lock()
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}
	mm.mu.Unlock()

	err := mm.InstallOrReplace(t.Context(), &entry, "int8-arm", srvURL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already being downloaded")
}

// TestVariantSelectable covers the accept path of the exported variant validator
// (the reject path is covered at the API layer).
func TestVariantSelectable(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	assert.True(t, VariantSelectable(&entry, ""), "an empty id selects the default variant")
	assert.True(t, VariantSelectable(&entry, "int8-arm"), "a real variant id is selectable")
	assert.False(t, VariantSelectable(&entry, "does-not-exist"))
}

// TestInstalledModelBasenameHint covers the registry-id -> settings-path mapping
// used by the scan tie-break.
func TestInstalledModelBasenameHint(t *testing.T) {
	t.Parallel()

	s := &conf.Settings{}
	s.Perch.ModelPath = "/models/perch-v2/perch_v2_int8_arm.onnx"
	assert.Equal(t, "perch_v2_int8_arm.onnx", installedModelBasenameHint(s, RegistryIDPerchV2, nil))
	assert.Empty(t, installedModelBasenameHint(s, RegistryIDBirdNETV3, nil), "a family with no recorded path yields no hint")
	assert.Empty(t, installedModelBasenameHint(nil, RegistryIDPerchV2, nil), "nil settings yields no hint")
}

// TestInstalledModelBasenameHint_PrefersLoadedPath pins the stale-variant
// follow-up: after a
// stale-path recovery the settings field still names the pre-recovery variant, so
// the gallery scan must key off the file the LOADED instance is actually running,
// not config.
func TestInstalledModelBasenameHint_PrefersLoadedPath(t *testing.T) {
	t.Parallel()

	s := &conf.Settings{}
	// Config still points at the stale, pre-recovery variant.
	s.Perch.ModelPath = "/stale/perch_v2_fp32.onnx"

	// A loaded instance whose resolved path is the RECOVERED variant wins over the
	// stale config: the scan must report the running variant.
	loaded := map[string]string{RegistryIDPerchV2: "/models/perch-v2/perch_v2_int8_arm.onnx"}
	assert.Equal(t, "perch_v2_int8_arm.onnx", installedModelBasenameHint(s, RegistryIDPerchV2, loaded),
		"the loaded instance's resolved path wins over the stale configured path")

	// A loaded instance running the built-in (empty resolved path) yields no hint
	// even though config still names a file: present-but-empty is authoritative.
	builtin := map[string]string{RegistryIDPerchV2: ""}
	assert.Empty(t, installedModelBasenameHint(s, RegistryIDPerchV2, builtin),
		"a loaded instance running the built-in yields no hint, not a fallback to the stale config")

	// A family ABSENT from the loaded map (no instance loaded: startup scan, or a
	// family the user disabled) falls back to the configured value.
	assert.Equal(t, "perch_v2_fp32.onnx", installedModelBasenameHint(s, RegistryIDPerchV2, map[string]string{}),
		"with no loaded instance the hint falls back to the configured field")
}
