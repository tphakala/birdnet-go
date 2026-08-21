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
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// isolateTestConfig points conf.ConfigPath at a throwaway file under t.TempDir()
// and restores the original in cleanup. Without it, applyConfigForPrimarySwap ->
// conf.SaveSettings resolves the default user config path and overwrites the
// developer's real ~/.config/birdnet-go/config.yaml during the test run.
func isolateTestConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))
	orig := conf.ConfigPath
	conf.ConfigPath = path
	t.Cleanup(func() { conf.ConfigPath = orig })
}

// permanentSwapEntry builds a synthetic permanent (BirdNET v2.4-style) catalog
// entry with a file-less BuiltIn baseline and one downloadable DFT-truncated
// variant served by a local test server. It mirrors the real birdnet-v2.4 shape
// (RegistryID == permanentRegistryID) so InstallOrReplace routes it through the
// dedicated primary-swap path, while using a small payload with a matching checksum
// so the download verifies without the real multi-MB model file.
func permanentSwapEntry(t *testing.T) (entry CatalogEntry, modelsDir, srvURL, dftLocalName string) {
	t.Helper()

	dftData := []byte("fake-dft-truncated-onnx-model")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dft.onnx" {
			_, _ = w.Write(dftData)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	dftLocalName = "BirdNET_v2.4_fp32_dfttrunc.onnx"
	entry = CatalogEntry{
		ID:              "test-primary-v2.4",
		Name:            "Test Primary v2.4",
		Version:         "2.4",
		Category:        CategoryBird,
		RegistryID:      permanentRegistryID,
		HuggingFaceRepo: "t/v2.4",
		SpeciesCount:    birdnetV24SpeciesCount,
		Variants: []CatalogVariant{
			{
				ID:           "builtin",
				BuiltIn:      true,
				Default:      true,
				SpeciesCount: birdnetV24SpeciesCount,
				Backends: map[string]BackendSupport{
					"tflite":          {Supported: true, Recommended: true},
					"onnxruntime-cpu": {Supported: true},
				},
			},
			{
				ID:        "fp32-dfttrunc",
				Precision: "fp32",
				Files: []CatalogFile{
					{RemotePath: "dft.onnx", LocalName: dftLocalName, Role: RoleModel, SHA256: sha256Hex(dftData), SizeBytes: int64(len(dftData))},
				},
			},
		},
	}
	return entry, t.TempDir(), srv.URL, dftLocalName
}

// TestModelManager_ScanInstalled_PermanentBaseline exercises the stale-file
// inversion in scanVariantEntry for the real birdnet-v2.4 entry: the BuiltIn
// baseline is always reported installed, a settings hint pointing at a DFT file
// present on disk promotes that variant, and a leftover DFT file with a cleared
// BirdNET.ModelPath must NOT be reported (the loader opens the embedded model).
func TestModelManager_ScanInstalled_PermanentBaseline(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	entry, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)
	dftLocal := modelRoleLocalName(t, variantByID(t, &entry, "fp32-dfttrunc").Files)

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	cases := []struct {
		name        string
		writeDFT    bool
		hintToDFT   bool
		wantVariant string
		wantPath    string // "" means empty (embedded baseline)
	}{
		{"no files, empty hint -> baseline", false, false, "builtin", ""},
		{"dft on disk + hint -> dft variant", true, true, "fp32-dfttrunc", "set"},
		{"stale dft on disk + empty hint -> baseline (inversion)", true, false, "builtin", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modelsDir := t.TempDir()
			dftPath := filepath.Join(modelsDir, entry.ID, dftLocal)
			if tc.writeDFT {
				require.NoError(t, os.MkdirAll(filepath.Dir(dftPath), 0o755))
				require.NoError(t, os.WriteFile(dftPath, []byte("dft"), 0o644))
			}

			settings := conftest.GetTestSettings()
			if tc.hintToDFT {
				settings.BirdNET.ModelPath = dftPath
			}
			conf.StoreSettings(settings)

			mm := NewModelManager(modelsDir, nil, settings)
			mm.ScanInstalled()

			im := installedByID(t, mm, entry.ID)
			assert.Equal(t, tc.wantVariant, im.VariantID, "resolved variant")
			if tc.wantPath == "" {
				assert.Empty(t, im.ModelPath, "baseline must report an empty model path")
			} else {
				assert.Equal(t, dftPath, im.ModelPath, "DFT variant must report its file path")
			}
		})
	}
}

// TestModelManager_PrimarySwap_BaselineToDFT verifies swapping the permanent
// primary model from its BuiltIn baseline to a DFT-truncated build: the file is
// downloaded, the install record and BirdNET.ModelPath point at it, and LabelPath is
// untouched. The orchestrator is nil, so the reload step is skipped (treated as a
// successful activation for the state-transition assertions).
func TestModelManager_PrimarySwap_BaselineToDFT(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	entry, modelsDir, srvURL, dftLocalName := permanentSwapEntry(t)

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.BirdNET.LabelPath = "/custom/labels.txt" // must survive the swap
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "fp32-dfttrunc", srvURL, nil))

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "fp32-dfttrunc", got.VariantID, "the DFT variant must be recorded")
	dftPath := filepath.Join(modelsDir, entry.ID, dftLocalName)
	assert.Equal(t, dftPath, got.ModelPath, "the install record must point at the downloaded file")
	assert.FileExists(t, dftPath, "the DFT model file must be on disk")

	current := conf.GetSettings()
	assert.Equal(t, dftPath, current.BirdNET.ModelPath, "BirdNET.ModelPath must select the DFT file")
	assert.Equal(t, "/custom/labels.txt", current.BirdNET.LabelPath, "a primary swap must never touch LabelPath")
	assert.Nil(t, mm.GetDownloadState(entry.ID), "no download state may linger after a completed swap")
}

// TestModelManager_PrimarySwap_DFTToBaseline verifies reverting from a
// DFT-truncated build back to the BuiltIn baseline: no download happens,
// BirdNET.ModelPath is cleared, the install record reports the baseline with an
// empty path, and the superseded DFT file is removed from disk.
func TestModelManager_PrimarySwap_DFTToBaseline(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	entry, modelsDir, srvURL, dftLocalName := permanentSwapEntry(t)

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	// Start on the DFT build.
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "fp32-dfttrunc", srvURL, nil))
	dftPath := filepath.Join(modelsDir, entry.ID, dftLocalName)
	require.FileExists(t, dftPath)
	require.Equal(t, dftPath, conf.GetSettings().BirdNET.ModelPath)

	// Revert to the baseline (empty variant id resolves to the default = builtin).
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "builtin", srvURL, nil))

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "builtin", got.VariantID, "the baseline must be recorded")
	assert.Empty(t, got.ModelPath, "the baseline install record carries no model path")

	current := conf.GetSettings()
	assert.Empty(t, current.BirdNET.ModelPath, "reverting to the baseline must clear BirdNET.ModelPath")

	_, err := os.Stat(dftPath)
	assert.True(t, os.IsNotExist(err), "the superseded DFT file must be removed when reverting to the baseline")
	assert.Nil(t, mm.GetDownloadState(entry.ID))
}

// TestModelManager_PrimarySwap_SameVariantIsNoOp verifies that selecting the
// already-active primary variant is an idempotent no-op that neither downloads nor
// changes config.
func TestModelManager_PrimarySwap_SameVariantIsNoOp(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	entry, modelsDir, srvURL, _ := permanentSwapEntry(t)

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)
	mm := NewModelManager(modelsDir, nil, settings)

	// Nothing installed yet: the permanent entry defaults to its BuiltIn baseline,
	// so requesting the baseline (by default and by id) is a no-op.
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "", srvURL, nil))
	require.NoError(t, mm.InstallOrReplace(t.Context(), &entry, "builtin", srvURL, nil))

	assert.Empty(t, conf.GetSettings().BirdNET.ModelPath, "a no-op baseline swap must not set a model path")
	assert.Nil(t, mm.GetDownloadState(entry.ID))
}

// TestModelManager_PrimarySwap_RollbackOnReloadFailure verifies the transactional
// rollback: when the primary reload fails, the swap restores the previous variant's
// record and config and removes the newly downloaded file. The reload is forced to
// fail with a primary-less test orchestrator (ReloadPrimaryForVariantSwap returns
// "primary model not available").
func TestModelManager_PrimarySwap_RollbackOnReloadFailure(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	entry, modelsDir, srvURL, dftLocalName := permanentSwapEntry(t)

	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)

	orch := newTestOrchestrator(t) // no primary -> ReloadPrimaryForVariantSwap errors
	mm := NewModelManager(modelsDir, orch, settings)

	err := mm.InstallOrReplace(t.Context(), &entry, "fp32-dfttrunc", srvURL, nil)
	require.Error(t, err, "a failed primary reload must surface as an error")

	got := installedByID(t, mm, entry.ID)
	assert.Equal(t, "builtin", got.VariantID, "a failed swap must restore the baseline record")

	current := conf.GetSettings()
	assert.Empty(t, current.BirdNET.ModelPath, "a failed swap must restore the (empty) baseline model path")

	dftPath := filepath.Join(modelsDir, entry.ID, dftLocalName)
	_, statErr := os.Stat(dftPath)
	assert.True(t, os.IsNotExist(statErr), "a failed swap must remove the newly downloaded file")
}
