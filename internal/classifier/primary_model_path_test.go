package classifier

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// writePrimaryGalleryModel installs a BirdNET v2.4 primary variant's model file
// into the gallery layout and returns its path. The primary family is
// model-only: applyConfigForPrimarySwap writes BirdNET.ModelPath and documents
// that it never touches BirdNET.LabelPath, because the v2.4 label set is
// embedded and identical across variants.
func writePrimaryGalleryModel(t *testing.T, modelsDir string) string {
	t.Helper()
	entry, ok := primaryCatalogEntry()
	require.True(t, ok, "the catalog must carry an entry for the primary registry ID")
	for i := range entry.Variants {
		v := &entry.Variants[i]
		for j := range v.Files {
			if v.Files[j].Role != RoleModel {
				continue
			}
			subdir := filepath.Join(modelsDir, entry.ID)
			require.NoError(t, os.MkdirAll(subdir, 0o755))
			p := filepath.Join(subdir, v.Files[j].LocalName)
			require.NoError(t, os.WriteFile(p, []byte("m"), 0o600))
			return p
		}
	}
	t.Fatalf("no primary catalog variant declares a model file")
	return ""
}

// primaryCatalogEntry returns the catalog entry for the primary classifier.
func primaryCatalogEntry() (CatalogEntry, bool) {
	catalog := ActiveCatalog()
	for i := range catalog {
		if catalog[i].RegistryID == permanentRegistryID && len(catalog[i].Variants) > 0 {
			return catalog[i], true
		}
	}
	return CatalogEntry{}, false
}

// TestResolvePrimaryModelPath covers the primary classifier's stale-path
// recovery. Before it, BirdNET.ModelPath was the one configured model path with
// no fallback at all: the Tier-3 branch took the configured string whenever it
// was non-empty and returned the os.ReadFile error, so under exactly the
// conditions GitHub #4201 and #4204 describe (a gallery variant switch, or a
// container HOME change that invalidates a stored absolute path) a user who had
// selected a DFT primary variant got a hard NewBirdNET failure. That is worse
// than the secondary case it mirrors: no analysis at all, rather than one
// missing optional model.
func TestResolvePrimaryModelPath(t *testing.T) {
	t.Parallel()

	t.Run("an empty configured path is left alone", func(t *testing.T) {
		t.Parallel()
		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		res := o.resolvePrimaryModelPath("")

		assert.Empty(t, res.resolved.model, "no configured path means the Tier-4 default, not a substitution")
		assert.False(t, res.substituted, "nothing the user chose was replaced")
		assert.False(t, res.repairable)
	})

	t.Run("a present configured path is used verbatim", func(t *testing.T) {
		t.Parallel()
		modelsDir := t.TempDir()
		installed := writePrimaryGalleryModel(t, modelsDir)

		dir := t.TempDir()
		configured := filepath.Join(dir, "my_own_primary.tflite")
		require.NoError(t, os.WriteFile(configured, []byte("m"), 0o600))

		o := &Orchestrator{}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(configured)

		assert.Equal(t, configured, res.resolved.model)
		assert.False(t, res.substituted)
		assert.NotEqual(t, installed, res.resolved.model,
			"a healthy custom primary must never be swapped for the installed gallery variant")
	})

	t.Run("a missing configured path recovers the installed variant", func(t *testing.T) {
		t.Parallel()
		modelsDir := t.TempDir()
		installed := writePrimaryGalleryModel(t, modelsDir)

		o := &Orchestrator{ortAvailable: func(string) bool { return true }}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		assert.Equal(t, installed, res.resolved.model,
			"a confirmed-absent primary path must recover the installed variant instead of failing the load")
		assert.True(t, res.substituted, "the user is running a model they did not configure")
		assert.True(t, res.repairable, "a CONFIRMED absence may repair config.yaml")
	})

	t.Run("recovery is refused when the installed variant's backend is unavailable", func(t *testing.T) {
		t.Parallel()
		modelsDir := t.TempDir()
		writePrimaryGalleryModel(t, modelsDir)

		// Every installed primary variant is an ONNX build, so on a host where
		// NEITHER backend can load one, recovering onto it converts a recoverable
		// stale path into a hard startup failure. That is the very outcome this
		// recovery exists to prevent, so it must fall through to the built-in
		// baseline instead.
		//
		// Both seams are stubbed, not just ORT. Stubbing ORT alone made this test
		// environment-dependent: it passed on a bare runner and FAILED under
		// -tags openvino on any host with a plannable OpenVINO device, a build CI
		// compiles but never runs tests for.
		o := &Orchestrator{
			ortAvailable: func(string) bool { return false },
			ovLoadable:   func(string) bool { return false },
		}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		assert.Empty(t, res.resolved.model,
			"without ONNX Runtime the installed variant cannot load, so the built-in baseline must be used")
		assert.True(t, res.substituted, "the user is still not running the model they configured")
		assert.False(t, res.repairable, "nothing correct to write, so the user's setting must survive")
	})

	t.Run("a missing configured path with nothing installed falls back to the built-in model", func(t *testing.T) {
		t.Parallel()
		// A models directory with no primary variant installed at all.
		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		assert.Empty(t, res.resolved.model,
			"resolving to empty is what makes Tier 4 fire and the built-in baseline boot; "+
				"keeping the stale path here is the hard-failure bug this fixes")
		assert.True(t, res.substituted, "running the baseline instead of the chosen model must not be silent")
		assert.False(t, res.repairable,
			"there is no correct path to write, so the user's own setting must survive for the next start")
	})

	t.Run("OpenVINO alone is enough to recover, with no ONNX Runtime", func(t *testing.T) {
		t.Parallel()
		modelsDir := t.TempDir()
		installed := writePrimaryGalleryModel(t, modelsDir)

		// An openvino-tagged build on an A76/Pi5 or an Intel iGPU runs these
		// variants with no ONNX Runtime installed at all. Gating on ORT alone
		// refused a variant that would have loaded, dropping such a host to the
		// embedded model while telling the user no installed model was available.
		//
		o := &Orchestrator{
			ortAvailable: func(string) bool { return false },
			ovLoadable:   func(string) bool { return true },
		}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		// Branch on whether a plan is actually OBTAINABLE here, not on the build tag.
		// The tag is necessary but not sufficient: openVINOPlanFor also needs a
		// usable device, so an openvino build on a plain amd64 runner or an ARMv8.0
		// core yields no plan. Gating on the tag alone would make this test pass on
		// this developer's machine and fail on those hosts, which is precisely the
		// inversion that shipped in the round it replaced.
		settings := o.currentSettings()
		if settings == nil {
			settings = &conf.Settings{}
		}
		_, planOK, _ := openVINOPlanFor(
			settings.BirdNET.Backend,
			settings.BirdNET.OpenVINODevice,
			DefaultModelVersion,
			settings.BirdNET.OpenVINOPath,
			birdnetLogitsOutputIndex,
		)
		if !planOK {
			assert.Empty(t, res.resolved.model,
				"with no obtainable OpenVINO plan there is no OpenVINO leg to take")
			return
		}
		assert.Equal(t, installed, res.resolved.model,
			"a loadable OpenVINO plan is sufficient; ONNX Runtime is only the fallback")
		assert.True(t, res.substituted)
		assert.True(t, res.repairable)
	})

	t.Run("an eligible but unloadable OpenVINO runtime does not count", func(t *testing.T) {
		t.Parallel()
		modelsDir := t.TempDir()
		writePrimaryGalleryModel(t, modelsDir)

		// initializeModel falls through to ONNX Runtime when OpenVINO is eligible
		// but fails to LOAD, and openVINOPlanFor's CPU branch answers yes from the
		// CPU's f16 support without ever opening the library. Accepting on
		// eligibility would hand a host with a broken OpenVINO library to the ONNX
		// path it has no runtime for.
		o := &Orchestrator{
			ortAvailable: func(string) bool { return false },
			ovLoadable:   func(string) bool { return false },
		}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		assert.Empty(t, res.resolved.model,
			"an OpenVINO plan that cannot load must not count as a usable backend")
		assert.True(t, res.substituted)
		assert.False(t, res.repairable)
	})

	t.Run("an unreadable configured path is kept, not substituted", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			// Same reason as TestResolveFamilyPaths_IndeterminateFallsBackWithoutRepair:
			// Windows maps the ENOTDIR-via-file-prefix stat to fs.ErrNotExist, which is
			// a CONFIRMED absence, so the indeterminate branch is unreachable here.
			t.Skip("ENOTDIR-via-file-prefix is ERROR_PATH_NOT_FOUND on Windows, mapped to fs.ErrNotExist")
		}

		modelsDir := t.TempDir()
		installed := writePrimaryGalleryModel(t, modelsDir)

		// A regular file used as a path prefix makes os.Stat return ENOTDIR, which
		// is indeterminate rather than a confirmed absence.
		dir := t.TempDir()
		regular := filepath.Join(dir, "not-a-dir")
		require.NoError(t, os.WriteFile(regular, []byte("x"), 0o600))
		configured := filepath.Join(regular, "primary.onnx")

		o := &Orchestrator{}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(configured)

		// This is the ONE place the primary deliberately diverges from the
		// secondaries, which DO fall back on this signal. A secondary that fails to
		// load costs one optional overlay for the session; silently swapping the
		// primary would attribute every detection written while the condition lasted
		// to the wrong model.
		assert.Equal(t, configured, res.resolved.model,
			"the primary must never switch models on an ambiguous stat error")
		assert.NotEqual(t, installed, res.resolved.model)
		assert.False(t, res.substituted)
		assert.False(t, res.repairable)
	})
}

// TestResolvePrimaryModelPath_EnvVarPathIsRecoveredButNotRepairable is separate
// from the table above because t.Setenv cannot run under a parallel parent.
func TestResolvePrimaryModelPath_EnvVarPathIsRecoveredButNotRepairable(t *testing.T) {
	modelsDir := t.TempDir()
	installed := writePrimaryGalleryModel(t, modelsDir)

	// A configured "$VAR/..." that is genuinely missing SHOULD still recover at
	// runtime, but must never be rewritten: flattening the variable into the
	// absolute path it expands to today makes the path stop following the variable
	// on the next container start, which is the exact fragility this recovery
	// exists to undo.
	t.Setenv("TEST_GONE_DIR", filepath.Join(t.TempDir(), "gone"))
	o := &Orchestrator{ortAvailable: func(string) bool { return true }}
	o.SetModelsDir(modelsDir)

	res := o.resolvePrimaryModelPath("$TEST_GONE_DIR/primary.onnx")

	assert.Equal(t, installed, res.resolved.model, "the runtime substitution still happens")
	assert.True(t, res.substituted)
	assert.False(t, res.repairable, "rewriting would flatten the variable into an absolute path")
}

// TestResolvePrimaryModelPath_ExpandsBeforeStat is separate from the table above
// because t.Setenv cannot run under a parallel parent.
func TestResolvePrimaryModelPath_ExpandsBeforeStat(t *testing.T) {
	modelsDir := t.TempDir()
	writePrimaryGalleryModel(t, modelsDir)

	dir := t.TempDir()
	modelFile := filepath.Join(dir, "primary.tflite")
	require.NoError(t, os.WriteFile(modelFile, []byte("m"), 0o600))
	t.Setenv("TEST_PRIMARY_DIR", dir)

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	res := o.resolvePrimaryModelPath("$TEST_PRIMARY_DIR/primary.tflite")

	// loadModel and initializeONNXModel both expand before opening. Statting the
	// RAW string here would classify every configured "$VAR/..." path as missing
	// and substitute a model out from under the user on every single start.
	assert.Equal(t, "$TEST_PRIMARY_DIR/primary.tflite", res.resolved.model,
		"the configured form is preserved; only the stat is done on the expanded path")
	assert.False(t, res.substituted, "an env-var path that resolves to a real file is not stale")
}

// TestNewBirdNET_NilResolverUsesConfiguredPathVerbatim pins the seam's default.
// Every construction without an orchestrator (tests, and any future direct
// caller) must behave exactly as it did before the resolver existed, or the
// change would silently alter which file those callers load.
func TestNewBirdNET_NilResolverUsesConfiguredPathVerbatim(t *testing.T) {
	t.Parallel()

	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.ModelPath = "/models/whatever.onnx"
	bn.primaryPath = resolvePrimaryOrConfigured(nil, bn.Settings.BirdNET.ModelPath)

	assert.Equal(t, "/models/whatever.onnx", bn.configuredModelPath(),
		"a nil resolver must pass the configured path through untouched")
}

// TestReloadModelInternal_RecoveredPathDoesNotVetoReload is the regression test
// for the second-order bug that split this work out of the earlier PR.
//
// reloadModelInternal re-derives the model identity from settings on every
// reload and refuses the reload when the result differs from the live
// ModelInfo.CustomPath. If startup recovers a stale primary path (making
// ModelInfo.CustomPath the RECOVERED file) while config.yaml still holds the
// stale string, then a reload that re-derived from the raw setting would compare
// recovered against stale, see a mismatch, roll back, and fail with "model
// identity changed: requires orchestrator restart". A user who hit the original
// stale-path bug would get a second, louder bug on their very next settings save
// (a locale change, a threshold edit).
//
// No native model is needed: an unreadable TaxonomyPath fails the reload at the
// first fallible step, which runs AFTER the identity switch. So the error text
// distinguishes the two outcomes precisely: reaching the taxonomy step proves
// the identity gate let the reload through, and a veto never gets that far.
func TestReloadModelInternal_RecoveredPathDoesNotVetoReload(t *testing.T) {
	newServing := func(t *testing.T, configuredPath, liveCustomPath string, resolve primaryPathResolver) *BirdNET {
		t.Helper()
		settings := conftest.GetTestSettings()
		settings.BirdNET.Version = ""
		settings.BirdNET.ModelPath = configuredPath
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		bn := &BirdNET{
			classifier:     &rollbackFakeClassifier{},
			Settings:       settings,
			ModelInfo:      customBirdNETV24ModelInfo(liveCustomPath),
			TaxonomyPath:   filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json"),
			speciesCache:   make(map[string]*speciesCacheEntry),
			resolvePrimary: resolve,
		}
		bn.primaryPath = pathResolution{resolved: modelFileSet{model: liveCustomPath}}
		bn.settingsAtomic.Store(settings)
		bn.publishIdentity()
		return bn
	}

	t.Run("a recovered path with stale config must not be refused", func(t *testing.T) {
		stale := "/gone/primary_dft.onnx"
		recovered := "/config/models/birdnet-v2.4/primary_dft.onnx"

		// The resolver recovers the stale configured path, exactly as it did at
		// startup. Config still carries the stale string because the correction is
		// persisted asynchronously, and may have failed to persist at all.
		bn := newServing(t, stale, recovered, func(configured string) pathResolution {
			require.Equal(t, stale, configured, "the reload must re-resolve the CONFIGURED path")
			return pathResolution{
				resolved:    modelFileSet{model: recovered},
				substituted: true,
				repairable:  true,
			}
		})

		err := bn.reloadModelInternal(false)

		require.Error(t, err, "the reload still fails at the taxonomy step; that is the probe, not the subject")
		assert.Contains(t, err.Error(), "taxonomy",
			"the reload must reach the taxonomy step, which proves the identity gate let it through")
		assert.NotContains(t, err.Error(), "requires orchestrator restart",
			"re-resolving the recovered path is what stops a recovered start from failing its next settings save")
	})

	t.Run("a genuine user edit to a different file is still refused", func(t *testing.T) {
		// The negative control. Without it the test above would pass just as well
		// against a gate that had been deleted outright.
		edited := "/srv/models/a_different_model.tflite"
		live := "/srv/models/the_old_model.tflite"

		bn := newServing(t, edited, live, func(configured string) pathResolution {
			return pathResolution{resolved: modelFileSet{model: configured}}
		})

		err := bn.reloadModelInternal(false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires orchestrator restart",
			"changing the primary model file is still a model-identity change")
	})
}

// TestPlanPathCorrection_PrimaryRepairsModelPathOnly pins the primary family's
// settings mapping. BirdNET.LabelPath must be left alone: the v2.4 label set is
// embedded and identical across variants, which is why applyConfigForPrimarySwap
// writes ModelPath alone and documents that a user-configured custom label path
// has to survive a variant swap. Repairing LabelPath here would break that
// contract from the other direction.
func TestPlanPathCorrection_PrimaryRepairsModelPathOnly(t *testing.T) {
	t.Parallel()

	entry, ok := primaryCatalogEntry()
	require.True(t, ok)

	modelsDir := filepath.Join(t.TempDir(), "models")
	installed := writePrimaryGalleryModel(t, modelsDir)

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// The same gallery layout under an OLD root that no longer exists: the
	// changed-container-HOME case, which is gallery-managed and so repairable.
	staleModel := filepath.Join(t.TempDir(), "models", entry.ID, filepath.Base(installed))
	customLabels := "/srv/my-models/my_own_labels.txt"

	current := &conf.Settings{}
	current.BirdNET.ModelPath = staleModel
	current.BirdNET.LabelPath = customLabels

	updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
		registryID: permanentRegistryID,
		resolved:   modelFileSet{model: installed},
		repairable: true,
	})

	require.Equal(t, correctionRewrite, outcome)
	assert.Equal(t, installed, updated.BirdNET.ModelPath, "the stale primary model path must be repaired")
	assert.Equal(t, customLabels, updated.BirdNET.LabelPath,
		"the primary family is model-only; a user's custom label path must never be rewritten")
}

// TestQueuePathCorrection_PrimaryFamily covers the queueing RULE the primary
// shares with the three secondaries, for the resolution shapes only the primary
// can produce (notably the built-in fallback, whose resolved model is empty).
//
// Named for what it tests: it calls queuePathCorrection directly and does NOT
// exercise NewOrchestrator's call site, which needs a real model to reach. The
// construction path that feeds it is covered by
// TestNewBirdNET_RecoversStaleConfiguredPath.
func TestQueuePathCorrection_PrimaryFamily(t *testing.T) {
	t.Parallel()

	t.Run("a substituted primary resolution is queued under the primary registry ID", func(t *testing.T) {
		t.Parallel()
		o := &Orchestrator{}
		o.queuePathCorrection(permanentRegistryID, pathResolution{
			resolved:    modelFileSet{model: "/models/birdnet-v2.4/recovered.onnx"},
			substituted: true,
			repairable:  true,
		})

		require.Len(t, o.pendingPathCorrections, 1)
		assert.Equal(t, permanentRegistryID, o.pendingPathCorrections[0].registryID,
			"a correction filed under any other ID would be planned against the wrong settings fields")
		assert.True(t, o.pendingPathCorrections[0].repairable)
	})

	t.Run("a clean primary resolution queues nothing", func(t *testing.T) {
		t.Parallel()
		o := &Orchestrator{}
		o.queuePathCorrection(permanentRegistryID, pathResolution{
			resolved: modelFileSet{model: "/models/configured.onnx"},
		})
		assert.Empty(t, o.pendingPathCorrections,
			"a healthy configured primary must never queue a repair or a notification")
	})

	t.Run("a built-in fallback is queued so the user is told, but is not repairable", func(t *testing.T) {
		t.Parallel()
		o := &Orchestrator{}
		o.queuePathCorrection(permanentRegistryID, pathResolution{substituted: true})

		require.Len(t, o.pendingPathCorrections, 1,
			"running the built-in model instead of the configured one must not be silent")
		assert.Empty(t, o.pendingPathCorrections[0].resolved.model)
		assert.False(t, o.pendingPathCorrections[0].repairable,
			"there is no correct path to write, so the user's setting must survive")
	})
}

// TestApplyPathCorrection_NotificationVariants covers the two message variants
// that had no test: the built-in fallback body, and the developer-facing
// unknown-family outcome that must NOT produce a user warning. Both were
// introduced with this change and both are user-visible (or deliberately not).
func TestApplyPathCorrection_NotificationVariants(t *testing.T) {
	// Not parallel: mutates conf globals, conf.ConfigPath and the notification
	// singleton.
	redirectConfigFile(t)
	SetPathCorrectionPersistenceDisabled(false)
	t.Cleanup(func() { SetPathCorrectionPersistenceDisabled(false) })

	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	stale := &conf.Settings{}
	stale.BirdNET.ModelPath = "/gone/primary_dft.onnx"
	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir())

	// Built-in fallback: confirmed absent, nothing installed, so resolved is empty.
	o.applyPathCorrection(&pendingPathCorrection{
		registryID: permanentRegistryID,
		resolved:   modelFileSet{},
		repairable: false,
	})

	// Unknown family: a self-heal gap, not a user problem.
	o.applyPathCorrection(&pendingPathCorrection{
		registryID: "Not_A_Model",
		resolved:   modelFileSet{model: "/models/whatever.onnx"},
		repairable: true,
	})

	list, err := svc.List(nil)
	require.NoError(t, err)
	var builtin, other int
	for _, n := range list {
		if n.MessageKey == notification.MsgModelPathBuiltinMessage {
			builtin++
			continue
		}
		other++
	}
	assert.Equal(t, 1, builtin,
		"the built-in fallback must name the built-in model, not an empty installed path")
	assert.Zero(t, other,
		"an unknown registry ID is a developer-facing gap and must never warn the user")

	after, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.NotContains(t, string(after), "/models/whatever.onnx",
		"an unknown family must never reach the config write")
}

// TestNewBirdNET_RecoversStaleConfiguredPath is the construction-level proof that
// the recovery reaches the model load, not just the resolver.
//
// It is the unit-level counterpart of the manual reproduction: with a stale
// birdnet.modelpath, the previous behaviour was a hard NewBirdNET failure, so the
// pipeline never started and there was no analysis at all. Every consumer on the
// load path (identity resolution, backend dispatch, the file open itself) has to
// agree on the RECOVERED file, so a version of this change that resolved the path
// but left any one consumer reading the raw setting still fails here.
func TestNewBirdNET_RecoversStaleConfiguredPath(t *testing.T) {
	t.Parallel()

	// Same reason as TestNewBirdNET_LocaleNormalization: the recovered file is a
	// TFLite v2.4 model, which a notflite build cannot load. See #1553.
	if !tfliteBackendAvailable {
		t.Skip("TFLite backend not linked (notflite build); this test recovers onto a TFLite v2.4 model")
	}

	stale := filepath.Join(t.TempDir(), "gone", "BirdNET_v2.4_fp32_dfttrunc.onnx")

	settings := conftest.GetTestSettings()
	settings.BirdNET.Version = ""
	settings.BirdNET.ModelPath = stale
	settings.BirdNET.Locale = "en-uk"

	var resolverCalls int
	bn, err := NewBirdNET(settings, nil, func(configured string) pathResolution {
		resolverCalls++
		assert.Equal(t, stale, configured, "the constructor must resolve the CONFIGURED path")
		return pathResolution{
			resolved:    modelFileSet{model: testV24TFLiteModelPath},
			substituted: true,
			repairable:  true,
		}
	})
	if bn != nil {
		t.Cleanup(bn.Delete)
	}

	require.NoError(t, err,
		"a stale configured primary path must recover, not abort construction; aborting is the total-analysis-loss bug")
	require.NotNil(t, bn)
	assert.Equal(t, 1, resolverCalls, "the constructor resolves exactly once, so the log and the stat are not doubled")

	assert.Equal(t, testV24TFLiteModelPath, bn.configuredModelPath(),
		"every load-path consumer reads this, so it must be the recovered file")
	assert.Equal(t, testV24TFLiteModelPath, bn.ModelInfo.CustomPath,
		"the identity must describe the file that was actually loaded, or the next reload vetoes itself")
	assert.NotEqual(t, stale, bn.ModelInfo.CustomPath)

	assert.True(t, bn.primaryPath.substituted)
	assert.True(t, bn.primaryPath.repairable)

	// The setting itself is never mutated: the repair goes through the correction
	// queue, which is what keeps a read-only diagnostic command (benchmark,
	// rangefilter print) from rewriting the user's configuration.
	assert.Equal(t, stale, settings.BirdNET.ModelPath,
		"settings.BirdNET.ModelPath must never be mutated in place by the recovery")
}

// TestReloadModelInternal_BuiltinFallbackSteadyStateReloadsCleanly is the
// regression test for a defect introduced while fixing this changeset's own
// review findings, and caught by the report-only review of that fix wave.
//
// After a successful startup recovery onto the built-in baseline, config keeps
// the stale path (that recovery is deliberately not repairable), so EVERY
// subsequent reload re-resolves to the same substituted-and-empty result. A veto
// keyed on `substituted` alone therefore failed every settings save forever, for
// exactly the users the recovery exists to rescue. The veto must key on the
// resolution having CHANGED from a real file to nothing.
func TestReloadModelInternal_BuiltinFallbackSteadyStateReloadsCleanly(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Version = ""
	settings.BirdNET.ModelPath = "/gone/primary_dft.onnx"
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	// The steady state: startup already resolved this away to the baseline, so the
	// live resolution is ALSO empty. Nothing changed between then and now.
	bn := &BirdNET{
		classifier:     &rollbackFakeClassifier{},
		Settings:       settings,
		ModelInfo:      stockPrimaryModelInfo(),
		TaxonomyPath:   filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json"),
		speciesCache:   make(map[string]*speciesCacheEntry),
		resolvePrimary: func(string) pathResolution { return pathResolution{substituted: true} },
	}
	bn.primaryPath = pathResolution{substituted: true}
	bn.settingsAtomic.Store(settings)
	bn.publishIdentity()

	err := bn.reloadModelInternal(false)

	require.Error(t, err, "the reload still fails at the taxonomy step; that is the probe, not the subject")
	assert.Contains(t, err.Error(), "taxonomy",
		"the reload must reach the taxonomy step, which proves the identity gate let it through")
	assert.NotContains(t, err.Error(), "requires orchestrator restart",
		"a steady-state baseline reload must not be refused, or every settings save fails forever")
}

// TestReloadModelInternal_VanishedRunningModelIsRefused is the positive half:
// when the file this instance is RUNNING goes away and nothing can replace it,
// the reload must be refused rather than silently loading the baseline under an
// identity that still names the vanished file.
func TestReloadModelInternal_VanishedRunningModelIsRefused(t *testing.T) {
	const (
		oldPath = "/data/previous_v24.tflite"
		newPath = "/data/just_saved_v24.tflite"
	)

	// The published snapshot is what the reload reads; bn.Settings is the previous
	// one that rollback restores. They must DIFFER, or the message could be built
	// from either and the read-after-rollback bug would be invisible.
	published := conftest.GetTestSettings()
	published.BirdNET.Version = ""
	published.BirdNET.ModelPath = newPath
	conftest.SetTestSettings(published)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	previous := conf.CloneSettings(published)
	previous.BirdNET.ModelPath = oldPath

	bn := &BirdNET{
		classifier:     &rollbackFakeClassifier{},
		Settings:       previous,
		ModelInfo:      customBirdNETV24ModelInfo(oldPath),
		TaxonomyPath:   filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json"),
		speciesCache:   make(map[string]*speciesCacheEntry),
		resolvePrimary: func(string) pathResolution { return pathResolution{substituted: true} },
	}
	// Was running a real custom file; the new resolution finds nothing.
	bn.primaryPath = pathResolution{resolved: modelFileSet{model: oldPath}}
	bn.settingsAtomic.Store(previous)
	bn.publishIdentity()

	err := bn.reloadModelInternal(false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires orchestrator restart",
		"loading the baseline under an identity naming the vanished file would misattribute every detection")
	assert.Contains(t, err.Error(), newPath,
		"the message must name the path being reloaded")
	assert.NotContains(t, err.Error(), oldPath,
		"rollback restores the previous settings snapshot, so reading the path after it names the OLD file "+
			"and tells the user a healthy path is unusable")
}

// TestReloadModelInternal_UnknownVersionNamesTheRequestedVersion pins the same
// read-after-rollback hazard at its sibling site. rollback() restores bn.Settings,
// so a message built afterwards reports the PREVIOUS, valid version as unknown, or
// an empty string when the user had not set one.
func TestReloadModelInternal_UnknownVersionNamesTheRequestedVersion(t *testing.T) {
	published := conftest.GetTestSettings()
	published.BirdNET.Version = "9.9"
	conftest.SetTestSettings(published)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	previous := conf.CloneSettings(published)
	previous.BirdNET.Version = "2.4"

	bn := &BirdNET{
		classifier:   &rollbackFakeClassifier{},
		Settings:     previous,
		ModelInfo:    ModelRegistry[DefaultModelVersion],
		TaxonomyPath: filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json"),
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.settingsAtomic.Store(previous)
	bn.publishIdentity()

	err := bn.reloadModelInternal(false)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "9.9", "the message must name the version the user actually typed")
	assert.NotContains(t, err.Error(), "2.4",
		"naming the previously valid version tells the user a working setting is unknown")
}

// TestPrimaryRegistryID covers the family gate that decides whether the recovery
// runs at all. Deleting that gate lets a stale BirdNET v3.0 primary path be
// "recovered" onto a v2.4 model file: a 32 kHz/5 s identity pinned to a
// 48 kHz/3 s model with a different label set.
func TestPrimaryRegistryID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		want    string
		recover bool
	}{
		{"empty version is the default v2.4 family", "", permanentRegistryID, true},
		{"explicit 2.4", "2.4", permanentRegistryID, true},
		{"3.0 is a different family", "3.0", RegistryIDBirdNETV3, false},
		{"an unknown version resolves to nothing", "9.9", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &conf.Settings{}
			settings.BirdNET.Version = tt.version

			got := primaryRegistryID(settings)
			assert.Equal(t, tt.want, got)

			// The gate as NewOrchestrator applies it, not just the helper: a nil
			// resolver is what stops a non-v2.4 primary from being recovered onto a
			// v2.4 model file, and what keeps the hard-wired permanentRegistryID
			// correction label from ever being attached to another family.
			o := &Orchestrator{}
			resolver := o.primaryPathResolverFor(settings)
			if tt.recover {
				assert.NotNil(t, resolver, "the v2.4 family must get the recovery")
			} else {
				assert.Nil(t, resolver,
					"only the v2.4 family may use a recovery whose target and correction label are both hard-wired to it")
			}
		})
	}
}

// TestEmitPathSubstitutedNotification_UnreadableIsNotDerivedFromRepairable pins
// the distinction the explicit unreadable flag exists for. Both records below are
// NOT repairable; only one of them describes a file that is present. Selecting the
// wording from repairable alone tells the user of an ABSENT file to go and check
// its permissions.
func TestEmitPathSubstitutedNotification_UnreadableIsNotDerivedFromRepairable(t *testing.T) {
	// Not parallel: mutates the global notification service.
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	// Present but unreadable: EACCES on a NAS mount.
	emitPathSubstitutedNotification(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   modelFileSet{model: "/models/perch-v2/perch_v2.onnx"},
		repairable: false,
		unreadable: true,
	})

	// Confirmed ABSENT, and declined for rewriting because the configured path is
	// written with a variable. Same repairable value, different truth.
	emitPathSubstitutedNotification(&pendingPathCorrection{
		registryID: permanentRegistryID,
		resolved:   modelFileSet{model: "/models/birdnet-v2.4/recovered.onnx"},
		repairable: false,
		unreadable: false,
	})

	list, err := svc.List(nil)
	require.NoError(t, err)
	var unreadable, notFound int
	for _, n := range list {
		switch n.MessageKey {
		case notification.MsgModelPathUnreadableMessage:
			unreadable++
		case notification.MsgModelPathSubstitutedMessage:
			notFound++
		}
	}
	assert.Equal(t, 1, unreadable, "only the present-but-unreadable file may say it could not be read")
	assert.Equal(t, 1, notFound,
		"an absent file must say it was not found, or the user checks permissions on a file that does not exist")
}
