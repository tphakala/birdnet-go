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

		o := &Orchestrator{}
		o.SetModelsDir(modelsDir)

		res := o.resolvePrimaryModelPath(filepath.Join(t.TempDir(), "gone", "primary.onnx"))

		assert.Equal(t, installed, res.resolved.model,
			"a confirmed-absent primary path must recover the installed variant instead of failing the load")
		assert.True(t, res.substituted, "the user is running a model they did not configure")
		assert.True(t, res.repairable, "a CONFIRMED absence may repair config.yaml")
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

// TestIsGalleryManagedPath_RejectsUnexpandedPath covers the guard that keeps the
// self-heal from flattening a variable out of a configured path. A configured
// "$HOME/models/<entry>/<file>" matches the gallery layout on base, parent and
// grandparent, so without the guard the repair would rewrite it to whatever it
// expands to today, and the path would then stop following HOME on the next
// container start. That is the exact fragility this self-heal exists to recover
// from.
func TestIsGalleryManagedPath_RejectsUnexpandedPath(t *testing.T) {
	// Not parallel: t.Setenv.
	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := filepath.Join(t.TempDir(), "models")
	installed := writeVariantModelFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	require.True(t, o.isGalleryManagedPath(RegistryIDPerchV2, installed),
		"the plain installed path is the control: it must qualify, or the cases below prove nothing")

	t.Setenv("TEST_ROOT", filepath.Dir(filepath.Dir(modelsDir)))
	base := filepath.Base(installed)

	withEnvVar := filepath.Join("$TEST_ROOT", "models", entry.ID, base)
	assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, withEnvVar),
		"an env-var path must never be taken over, or the repair flattens the variable")

	withTilde := filepath.Join("~", "models", entry.ID, base)
	assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, withTilde),
		"a ~-prefixed path must never be taken over for the same reason")
}

// TestNewBirdNET_RecoversStaleConfiguredPath is the construction-level proof
// that the recovery actually reaches the model load, not just the resolver.
//
// It is the unit-level counterpart of the manual reproduction: with a stale
// birdnet.modelpath, the previous behaviour was a hard NewBirdNET failure, so
// the pipeline never started and there was no analysis at all. Every consumer on
// the load path (identity resolution, backend dispatch, the file open itself)
// has to agree on the RECOVERED file, so a version of this change that resolved
// the path but left any one consumer reading the raw setting still fails here.
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

	// The orchestrator reads these to queue the config repair without resolving a
	// second time.
	assert.True(t, bn.primaryPath.substituted)
	assert.True(t, bn.primaryPath.repairable)

	// The setting itself is never mutated: the repair goes through the correction
	// queue, which is what keeps a read-only diagnostic command (benchmark,
	// rangefilter print) from rewriting the user's configuration.
	assert.Equal(t, stale, settings.BirdNET.ModelPath,
		"settings.BirdNET.ModelPath must never be mutated in place by the recovery")
}
