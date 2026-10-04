package classifier

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// All tests here use newDepHarness, which replaces the package-global catalog and
// settings, so none of them is parallel.

func TestInstall_DownloadsDependencyFilesAndRecordsThem(t *testing.T) {
	h := newDepHarness(t)

	require.NoError(t, h.install(depIDA, ""))

	assert.FileExists(t, h.own(depIDA, depIDA+"-model.onnx"))
	assert.FileExists(t, h.own(depIDA, depIDA+"-labels.txt"))
	assert.FileExists(t, h.shared(depLocalGeoModel))
	assert.FileExists(t, h.shared(depLocalGeoLabels))
	assert.FileExists(t, h.shared(depLocalTaxonomy))

	got := installedSnapshot(h.mm)
	require.Contains(t, got, depIDA)
	require.Contains(t, got, depIDG, "the geomodel dependency must be recorded as installed")
	require.Contains(t, got, depIDT, "the taxonomy component must be recorded as installed")
	assert.Equal(t, h.shared(depLocalGeoModel), got[depIDG].ModelPath)
	assert.Equal(t, h.shared(depLocalGeoLabels), got[depIDG].LabelsPath)
	assert.Equal(t, h.shared(depLocalTaxonomy), got[depIDT].ModelPath, "a taxonomy-only entry records its first file")

	rf := conf.GetSettings().RangeFilterConfig()
	assert.Equal(t, depGeomodelVer, rf.Model)
	assert.Equal(t, h.shared(depLocalGeoModel), rf.ModelPath)
	assert.Equal(t, h.shared(depLocalGeoLabels), rf.LabelsPath)

	for _, remote := range []string{"g-model.onnx", "g-labels.txt", "t-taxonomy.csv", depIDA + "-model.onnx"} {
		assert.Equal(t, 1, h.srv.Hits(remote), remote)
	}
}

func TestInstall_DependencyAlreadyOnDiskIsNotRefetched(t *testing.T) {
	h := newDepHarness(t)
	h.writeShared(depLocalGeoModel, "g-model.onnx")
	h.writeShared(depLocalGeoLabels, "g-labels.txt")

	require.NoError(t, h.install(depIDA, ""))

	assert.Zero(t, h.srv.Hits("g-model.onnx"))
	assert.Zero(t, h.srv.Hits("g-labels.txt"))
	assert.Equal(t, 1, h.srv.Hits("t-taxonomy.csv"))
	assert.Contains(t, installedSnapshot(h.mm), depIDG, "a dependency already on disk is still recorded")
}

func TestInstall_RefetchesOnlyCorruptDependencyFile(t *testing.T) {
	h := newDepHarness(t)
	h.writeShared(depLocalGeoModel, "g-model.onnx")
	h.writeShared(depLocalGeoLabels, "g-labels.txt")
	corrupt := h.shared(depLocalTaxonomy)
	require.NoError(t, os.WriteFile(corrupt, []byte("corrupt"), 0o644))

	require.NoError(t, h.install(depIDA, ""))

	assert.Zero(t, h.srv.Hits("g-model.onnx"))
	assert.Equal(t, 1, h.srv.Hits("t-taxonomy.csv"), "only the corrupt file is fetched again")
	data, err := os.ReadFile(corrupt)
	require.NoError(t, err)
	assert.Equal(t, depPayload("t-taxonomy.csv"), data)
}

func TestInstall_FailureRemovesDependencyFilesItDownloaded(t *testing.T) {
	h := newDepHarness(t)
	h.srv.Fail("t-taxonomy.csv")

	require.Error(t, h.install(depIDA, ""))

	assert.NoFileExists(t, h.own(depIDA, depIDA+"-model.onnx"))
	assert.NoFileExists(t, h.shared(depLocalGeoModel))
	assert.NoFileExists(t, h.shared(depLocalGeoLabels))
	assert.NoFileExists(t, h.shared(depLocalTaxonomy))
	assert.Empty(t, installedSnapshot(h.mm), "a failed install records nothing")
}

func TestInstall_DoesNotRecordDependencyWithActiveDirectInstall(t *testing.T) {
	h := newDepHarness(t)
	h.setDownloading(depIDG, StatusDownloading)

	require.NoError(t, h.install(depIDA, ""))

	got := installedSnapshot(h.mm)
	assert.NotContains(t, got, depIDG, "the direct install owns its own record")
	assert.Contains(t, got, depIDT)
	assert.FileExists(t, h.shared(depLocalGeoModel))
}

func TestInstall_RecordsDependencyDespiteFailedDirectInstall(t *testing.T) {
	h := newDepHarness(t)
	h.setDownloading(depIDG, StatusFailed)

	require.NoError(t, h.install(depIDA, ""))

	assert.Contains(t, installedSnapshot(h.mm), depIDG, "a failed, retained download does not own the record")
}

func TestScanInstalled_AdoptsSharedOnlyNonGeomodelEntry(t *testing.T) {
	h := newDepHarness(t)
	h.writeShared(depLocalTaxonomy, "t-taxonomy.csv")

	got := h.scanFresh()

	require.Contains(t, got, depIDT)
	assert.Equal(t, h.shared(depLocalTaxonomy), got[depIDT].ModelPath)
	assert.NotContains(t, got, depIDG, "a geomodel with absent files is not installed")
}

func TestInstallRecordMatchesScan(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))

	requireSameRecords(t, installedSnapshot(h.mm), h.scanFresh(), "a fresh scan must reproduce the install records")
}

func TestPrimarySwap_ToDFTInstallsDependencyAndPointsRangeFilter(t *testing.T) {
	h := newDepHarness(t)

	require.NoError(t, h.install(depIDP, depVariantDFT))

	got := installedSnapshot(h.mm)
	assert.Equal(t, depVariantDFT, got[depIDP].VariantID)
	require.Contains(t, got, depIDG)
	assert.Equal(t, h.shared(depLocalGeoModel), got[depIDG].ModelPath)
	assert.FileExists(t, h.shared(depLocalGeoModel))
	assert.FileExists(t, h.shared(depLocalGeoLabels))

	s := conf.GetSettings()
	assert.Equal(t, h.own(depIDP, depLocalDFT), s.BirdNET.ModelPath)
	rf := s.RangeFilterConfig()
	assert.Equal(t, depGeomodelVer, rf.Model)
	assert.Equal(t, h.shared(depLocalGeoModel), rf.ModelPath)
	assert.Equal(t, h.shared(depLocalGeoLabels), rf.LabelsPath)
	assert.Equal(t, 1, h.reloads, "the range filter reloads once after the swap")
}

func TestPrimarySwap_ToDFTKeepsCustomRangeFilter(t *testing.T) {
	h := newDepHarness(t)
	custom := conf.CloneSettings(conf.GetSettings())
	custom.RangeFilterConfig().ModelPath = "/custom/range.onnx"
	custom.RangeFilterConfig().Model = "custom"
	conf.StoreSettings(custom)

	require.NoError(t, h.install(depIDP, depVariantDFT))

	rf := conf.GetSettings().RangeFilterConfig()
	assert.Equal(t, "/custom/range.onnx", rf.ModelPath, "a custom range filter path is never overwritten")
	assert.Equal(t, "custom", rf.Model)
	assert.Contains(t, installedSnapshot(h.mm), depIDG, "the dependency is still recorded")
}

func TestPrimarySwap_ToBuiltinInstallsAndRecordsMissingDependency(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDP, depVariantDFT))
	// The geomodel disappears out of band: the baseline, like any variant, brings it back.
	require.NoError(t, os.Remove(h.shared(depLocalGeoModel)))
	require.NoError(t, os.Remove(h.shared(depLocalGeoLabels)))
	h.mm.mu.Lock()
	delete(h.mm.installed, depIDG)
	h.mm.mu.Unlock()
	hitsBefore := h.srv.Hits("g-model.onnx")

	require.NoError(t, h.install(depIDP, depVariantBuiltin))

	assert.Equal(t, hitsBefore+1, h.srv.Hits("g-model.onnx"), "the missing geomodel is fetched again")
	assert.FileExists(t, h.shared(depLocalGeoModel))
	got := installedSnapshot(h.mm)
	assert.Contains(t, got, depIDG)
	assert.Equal(t, depVariantBuiltin, got[depIDP].VariantID)
	assert.Empty(t, got[depIDP].ModelPath, "the baseline keeps an empty model path")
	assert.Empty(t, conf.GetSettings().BirdNET.ModelPath, "the embedded model stays selected")
	assert.Equal(t, h.shared(depLocalGeoModel), conf.GetSettings().RangeFilterConfig().ModelPath)
	assert.NoFileExists(t, h.own(depIDP, depLocalDFT), "the superseded DFT file is removed")
}

func TestReplaceVariant_LoadedSecondaryWithDependencyReloadsRangeFilter(t *testing.T) {
	// Not parallel: registers a global builder, mutates global settings and the catalog.
	srv := newDepServer(t)
	entry := CatalogEntry{
		ID: "test-dep-secondary", Version: "1.0", HuggingFaceRepo: "t/s",
		RegistryID: gaplessSecondaryID, DependsOn: []string{depIDG},
		Variants: []CatalogVariant{
			{ID: "v1", Default: true, Files: []CatalogFile{depFile(RoleModel, "s1.onnx", "model.onnx")}},
			{ID: "v2", Files: []CatalogFile{depFile(RoleModel, "s2.onnx", "model_v2.onnx")}},
		},
	}
	catalog := dependencyTestCatalog()
	catalog = append(catalog, entry)
	setActiveCatalog(catalog)
	t.Cleanup(func() { setActiveCatalog(nil) })
	resolved, ok := GetCatalogEntry(entry.ID)
	require.True(t, ok)

	modelsDir := t.TempDir()
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	conf.StoreSettings(settings)

	old := &reloadFakeModel{id: gaplessSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[gaplessSecondaryID] = &modelEntry{instance: old}
	o.SetModelsDir(modelsDir)
	newInst := &reloadFakeModel{id: gaplessSecondaryID}
	registerTestSecondaryBuilder(t, gaplessSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return newInst, nil
	})

	reloads := 0
	mm := NewModelManager(modelsDir, o, settings)
	mm.reloadRangeFilterFn = func() error { reloads++; return nil }
	mm.mu.Lock()
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}
	mm.mu.Unlock()

	old0 := &InstalledModel{CatalogID: entry.ID, VariantID: "v1", ModelPath: filepath.Join(modelsDir, entry.ID, "model.onnx")}
	require.NoError(t, mm.replaceVariant(t.Context(), &resolved, old0, "v2", srv.URL, nil))

	assert.Same(t, ModelInstance(newInst), o.models[gaplessSecondaryID].instance, "the new instance is swapped in gaplessly")
	assert.Equal(t, 1, reloads, "the geomodel dependency triggers a range filter reload")
	rf := conf.GetSettings().RangeFilterConfig()
	assert.Equal(t, depGeomodelVer, rf.Model)
	assert.Equal(t, filepath.Join(modelsDir, sharedDirName, depLocalGeoModel), rf.ModelPath)
	assert.Contains(t, installedSnapshot(mm), depIDG, "the dependency is recorded by the swap")
}

// requireDependents asserts err is a *DependentsError for catalogID naming ids.
func requireDependents(t *testing.T, err error, catalogID string, ids ...string) {
	t.Helper()
	de, ok := errors.AsType[*DependentsError](err)
	require.True(t, ok, "want *DependentsError, got %v", err)
	assert.Equal(t, catalogID, de.CatalogID)
	got := make([]string, 0, len(de.Dependents))
	for _, d := range de.Dependents {
		got = append(got, d.ID)
		assert.NotEmpty(t, d.Name)
	}
	assert.Equal(t, ids, got)
}

func TestUninstall_RefusesWhileDependentInstalled(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))
	before := installedSnapshot(h.mm)
	settingsBefore := conf.GetSettings()
	h.reloads = 0

	err := h.mm.Uninstall(depIDG)

	requireDependents(t, err, depIDG, depIDA)
	assert.Contains(t, err.Error(), "Test dep-a")
	assert.Equal(t, before, installedSnapshot(h.mm), "a refusal changes nothing")
	assert.FileExists(t, h.shared(depLocalGeoModel))
	assert.FileExists(t, h.shared(depLocalGeoLabels))
	assert.Same(t, settingsBefore, conf.GetSettings(), "a refusal does not touch settings")
	assert.Zero(t, h.reloads)
}

func TestUninstall_BuiltinRecordIsADependent(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	h.mm.mu.Lock()
	h.mm.installed[depIDP] = InstalledModel{CatalogID: depIDP, VariantID: depVariantBuiltin}
	h.mm.mu.Unlock()

	requireDependents(t, h.mm.Uninstall(depIDG), depIDG, depIDP)
	assert.FileExists(t, h.shared(depLocalGeoModel))
}

func TestUninstall_DFTRecordIsADependent(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDP, depVariantDFT))

	requireDependents(t, h.mm.Uninstall(depIDG), depIDG, depIDP)
}

func TestUninstall_KeepsComponentWhileAnotherDependentInstalled(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDB, ""))
	require.NoError(t, h.install(depIDA, ""))

	require.NoError(t, h.mm.Uninstall(depIDA))

	assert.Contains(t, installedSnapshot(h.mm), depIDT)
	assert.FileExists(t, h.shared(depLocalTaxonomy))
	assert.FileExists(t, h.shared(depLocalGeoModel))
}

func TestUninstall_AutoremovesUnusedComponent(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))

	require.NoError(t, h.mm.Uninstall(depIDA))

	got := installedSnapshot(h.mm)
	assert.NotContains(t, got, depIDA)
	assert.NotContains(t, got, depIDT, "the unused component is removed")
	assert.NoFileExists(t, h.shared(depLocalTaxonomy))
	assert.Contains(t, got, depIDG, "a visible dependency stays installed")
	assert.FileExists(t, h.shared(depLocalGeoModel))
	assert.FileExists(t, h.shared(depLocalGeoLabels))
	assert.Equal(t, depGeomodelVer, conf.GetSettings().RangeFilterConfig().Model, "the range filter keeps pointing at the kept geomodel")
	assert.Equal(t, h.shared(depLocalGeoModel), conf.GetSettings().RangeFilterConfig().ModelPath)
}

func TestUninstall_ActiveDownloadKeepsSharedFile(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))
	h.setDownloading(depIDB, StatusDownloading)

	require.NoError(t, h.mm.Uninstall(depIDA))

	assert.FileExists(t, h.shared(depLocalTaxonomy), "an active download of B still needs the taxonomy")
	assert.Contains(t, installedSnapshot(h.mm), depIDT)
}

func TestUninstall_FailedDownloadDoesNotKeepFiles(t *testing.T) {
	h := newDepHarness(t)
	// A classifier that inlines the same geomodel files, whose own download failed:
	// the failed (retained) state must not keep the geomodel files alive.
	inline := depClassifier("dep-inline", RegistryIDBSG)
	inline.DependsOn = nil
	inline.Files = append(inline.Files,
		depFile(RoleGeomodelModel, "g-model.onnx", depLocalGeoModel),
		depFile(RoleGeomodelLabels, "g-labels.txt", depLocalGeoLabels))
	setActiveCatalog(append(dependencyTestCatalog(), inline))
	require.NoError(t, h.install(depIDG, ""))
	h.setDownloading(inline.ID, StatusFailed)

	require.NoError(t, h.mm.Uninstall(depIDG))

	assert.NoFileExists(t, h.shared(depLocalGeoModel), "a failed download keeps nothing alive")
	assert.NoFileExists(t, h.shared(depLocalGeoLabels))
	assert.NotContains(t, h.scanFresh(), depIDG, "a rescan does not adopt the removed geomodel")
}

func TestUninstall_RefusesWhileDependentDownloading(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	h.setDownloading(depIDA, StatusDownloading)

	requireDependents(t, h.mm.Uninstall(depIDG), depIDG, depIDA)
	assert.FileExists(t, h.shared(depLocalGeoModel))
}

func TestUninstall_RangeFilterReloadFailureKeepsGeomodelFiles(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	h.reloadErr = errors.NewStd("reload failed")
	h.reloads = 0

	require.NoError(t, h.mm.Uninstall(depIDG))

	assert.Equal(t, 1, h.reloads)
	assert.FileExists(t, h.shared(depLocalGeoModel), "the geomodel files stay while the session may hold them")
	assert.FileExists(t, h.shared(depLocalGeoLabels))
	assert.NotContains(t, installedSnapshot(h.mm), depIDG)
}

// depStep is one move in the lifecycle transition table.
type depStep struct {
	op string // install, uninstall, swapDFT, swapBuiltin, rescan
	id string
}

// expectRefusal is the oracle for Uninstall(catalogID): refused exactly when an
// installed entry depends on it.
func (h *depHarness) expectRefusal(catalogID string) bool {
	for id := range installedSnapshot(h.mm) {
		e, ok := GetCatalogEntry(id)
		if !ok || id == catalogID {
			continue
		}
		if slices.Contains(e.DependsOn, catalogID) {
			return true
		}
	}
	return false
}

// assertDependencyInvariants checks, after every step: every installed
// dependent has all its effective files on disk, an installed component has its
// file, and a fresh scan reproduces mm.installed.
func (h *depHarness) assertDependencyInvariants(t *testing.T, step string) {
	t.Helper()
	got := installedSnapshot(h.mm)
	for id, rec := range got {
		e := h.entry(id)
		files, ok := EffectiveFiles(&e, rec.VariantID)
		require.True(t, ok, "%s: %s variant resolves", step, id)
		for _, f := range files {
			path := h.own(id, f.LocalName)
			if isSharedRole(f.Role) {
				path = h.shared(f.LocalName)
			}
			assert.FileExists(t, path, "%s: %s needs %s", step, id, f.LocalName)
		}
	}
	requireSameRecords(t, got, h.scanFresh(), step+": a rescan must reproduce mm.installed")
}

func TestDependencyLifecycle_Transitions(t *testing.T) {
	sequences := map[string][]depStep{
		"install A, install B, uninstall A, uninstall B": {
			{"install", depIDA}, {"install", depIDB}, {"uninstall", depIDA}, {"uninstall", depIDB},
		},
		"install B, install A, uninstall B, rescan, uninstall A": {
			{"install", depIDB}, {"install", depIDA}, {"uninstall", depIDB}, {"rescan", ""}, {"uninstall", depIDA},
		},
		"swap P to DFT, uninstall G refused, swap P to builtin, uninstall G refused": {
			{"swapDFT", ""}, {"uninstall", depIDG}, {"swapBuiltin", ""}, {"uninstall", depIDG},
		},
		"install A, uninstall G refused, uninstall A, uninstall G": {
			{"install", depIDA}, {"uninstall", depIDG}, {"uninstall", depIDA}, {"uninstall", depIDG},
		},
	}
	for name, steps := range sequences {
		for _, rescanBeforeUninstall := range []bool{false, true} {
			label := name
			if rescanBeforeUninstall {
				label += " (rescan before every uninstall)"
			}
			t.Run(label, func(t *testing.T) {
				h := newDepHarness(t)
				for i, s := range steps {
					if s.op == "uninstall" && rescanBeforeUninstall {
						h.rescan()
					}
					switch s.op {
					case "install":
						require.NoError(t, h.install(s.id, ""), "step %d %s %s", i, s.op, s.id)
					case "swapDFT":
						require.NoError(t, h.install(depIDP, depVariantDFT), "step %d", i)
					case "swapBuiltin":
						require.NoError(t, h.install(depIDP, depVariantBuiltin), "step %d", i)
					case "rescan":
						h.rescan()
					case "uninstall":
						refuse := h.expectRefusal(s.id)
						err := h.mm.Uninstall(s.id)
						if refuse {
							requireDependents(t, err, s.id, dependentsOracle(h, s.id)...)
						} else {
							require.NoError(t, err, "step %d %s %s", i, s.op, s.id)
						}
					}
					h.assertDependencyInvariants(t, label)
				}
			})
		}
	}
}

// dependentsOracle lists the installed dependents of catalogID, sorted.
func dependentsOracle(h *depHarness, catalogID string) []string {
	var out []string
	for id := range installedSnapshot(h.mm) {
		e := h.entry(id)
		if slices.Contains(e.DependsOn, catalogID) {
			out = append(out, id)
		}
	}
	// installedSnapshot iterates a map; the error lists dependents sorted by ID.
	slices.Sort(out)
	return out
}

func TestDependencyLifecycle_SecondInstallIsRefusedAndKeepsFiles(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))
	paths := []string{h.own(depIDA, depIDA+"-model.onnx"), h.shared(depLocalGeoModel), h.shared(depLocalTaxonomy)}
	mtimes := make([]time.Time, len(paths))
	for i, p := range paths {
		info, err := os.Stat(p)
		require.NoError(t, err)
		mtimes[i] = info.ModTime()
	}

	require.Error(t, h.install(depIDA, ""), "a second install of an installed entry is refused")

	for i, p := range paths {
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.True(t, mtimes[i].Equal(info.ModTime()), "%s must not be rewritten", p)
	}
	h.assertDependencyInvariants(t, "second install")
}

func TestUninstall_RetentionFollowsTheInstalledVariant(t *testing.T) {
	// V declares the taxonomy file inline in v1 only. Whether removing the last
	// dependent of the taxonomy component deletes the file depends on which variant of
	// V is installed.
	variantEntry := func() CatalogEntry {
		return CatalogEntry{
			ID: "dep-variant", Name: "Variant model", Version: "1.0", Category: CategoryBird, HuggingFaceRepo: "t/v",
			Variants: []CatalogVariant{
				{ID: "v1", Default: true, Files: []CatalogFile{
					depFile(RoleModel, "v1.onnx", "v1.onnx"),
					depFile(RoleTaxonomy, "t-taxonomy.csv", depLocalTaxonomy),
				}},
				{ID: "v2", Files: []CatalogFile{depFile(RoleModel, "v2.onnx", "v2.onnx")}},
			},
		}
	}
	for _, tc := range []struct {
		variant  string
		wantFile bool
	}{
		{"v1", true},
		{"v2", false},
	} {
		t.Run("installed variant "+tc.variant, func(t *testing.T) {
			h := newDepHarness(t)
			setActiveCatalog(append(dependencyTestCatalog(), variantEntry()))
			require.NoError(t, h.install(depIDA, ""))
			h.mm.mu.Lock()
			h.mm.installed["dep-variant"] = InstalledModel{CatalogID: "dep-variant", VariantID: tc.variant}
			h.mm.mu.Unlock()

			require.NoError(t, h.mm.Uninstall(depIDA))

			assert.NotContains(t, installedSnapshot(h.mm), depIDT, "the component record is dropped")
			if tc.wantFile {
				assert.FileExists(t, h.shared(depLocalTaxonomy), "the installed variant still names the file")
			} else {
				assert.NoFileExists(t, h.shared(depLocalTaxonomy), "the installed variant does not name the file")
			}
		})
	}
}

func TestInstall_FailureKeepsDependencyFilesAlreadyOnDisk(t *testing.T) {
	h := newDepHarness(t)
	geo := h.writeShared(depLocalGeoModel, "g-model.onnx")
	geoLabels := h.writeShared(depLocalGeoLabels, "g-labels.txt")
	h.srv.Fail("t-taxonomy.csv")

	require.Error(t, h.install(depIDA, ""))

	assert.FileExists(t, geo, "a verified file that was already there is not this call's to delete")
	assert.FileExists(t, geoLabels)
	assert.NoFileExists(t, h.own(depIDA, depIDA+"-model.onnx"))
	assert.Empty(t, installedSnapshot(h.mm))
}

func TestPrimarySwap_FailureFetchingDependencyChangesNothing(t *testing.T) {
	h := newDepHarness(t)
	h.srv.Fail("g-model.onnx")
	rfBefore := *conf.GetSettings().RangeFilterConfig()

	require.Error(t, h.install(depIDP, depVariantDFT))

	assert.NoFileExists(t, h.own(depIDP, depLocalDFT), "the variant file is removed")
	assert.NoFileExists(t, h.shared(depLocalGeoModel))
	assert.NotContains(t, installedSnapshot(h.mm), depIDG)
	assert.NotEqual(t, depVariantDFT, installedSnapshot(h.mm)[depIDP].VariantID)
	assert.Empty(t, conf.GetSettings().BirdNET.ModelPath, "the model path is not switched")
	assert.Equal(t, rfBefore, *conf.GetSettings().RangeFilterConfig(), "the range filter is not re-pointed")
}

func TestUninstall_FailedDependentDownloadDoesNotRefuse(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	h.setDownloading(depIDA, StatusFailed)

	require.NoError(t, h.mm.Uninstall(depIDG), "a failed, retained download is not a dependent")

	assert.NoFileExists(t, h.shared(depLocalGeoModel))
}

func TestInstall_FailureKeepsRefetchedFileOfRecordedDependency(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDG, ""))
	geo := h.shared(depLocalGeoModel)
	require.NoError(t, os.WriteFile(geo, []byte("corrupt"), 0o644))
	h.srv.Fail("t-taxonomy.csv")

	require.Error(t, h.install(depIDA, ""))

	assert.Equal(t, 2, h.srv.Hits("g-model.onnx"), "the corrupt geomodel file is refetched")
	assert.FileExists(t, geo, "a failed install keeps a refetched file the installed geomodel still needs")
	assert.Contains(t, installedSnapshot(h.mm), depIDG)
	assert.NoFileExists(t, h.own(depIDA, depIDA+"-model.onnx"), "the failed install's own files are removed")
}

func TestUninstall_DependentNeverDeletesDependencyFiles(t *testing.T) {
	h := newDepHarness(t)
	require.NoError(t, h.install(depIDA, ""))
	// The geomodel files are on disk but G has no record (as after a failed direct
	// install raced a dependent): uninstalling A must still leave them alone.
	h.mm.mu.Lock()
	delete(h.mm.installed, depIDG)
	h.mm.mu.Unlock()

	require.NoError(t, h.mm.Uninstall(depIDA))

	assert.FileExists(t, h.shared(depLocalGeoModel), "only the geomodel's own uninstall deletes its files")
	assert.FileExists(t, h.shared(depLocalGeoLabels))
	assert.NoFileExists(t, h.shared(depLocalTaxonomy), "the unused component is still autoremoved")
}
