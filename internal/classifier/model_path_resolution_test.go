package classifier

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// labelsRoleLocalName returns the labels file name a catalog variant declares.
func labelsRoleLocalName(t *testing.T, files []CatalogFile) string {
	t.Helper()
	for _, f := range files {
		if f.Role == RoleLabels {
			return f.LocalName
		}
	}
	t.Fatalf("variant declares no labels file")
	return ""
}

// writeVariantLabelsFile writes a placeholder labels file for a catalog variant
// and returns its path.
func writeVariantLabelsFile(t *testing.T, modelsDir string, entry *CatalogEntry, variantID string) string {
	t.Helper()
	v := variantByID(t, entry, variantID)
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	labelsPath := filepath.Join(subdir, labelsRoleLocalName(t, v.Files))
	require.NoError(t, os.WriteFile(labelsPath, []byte("Species1\n"), 0o600))
	return labelsPath
}

// TestResolveFamilyPaths_ConfiguredSetPresentIsUsedVerbatim asserts that a
// complete, on-disk configured set is honoured and no gallery lookup replaces
// it, so a user's custom model keeps working.
func TestResolveFamilyPaths_ConfiguredSetPresentIsUsedVerbatim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	model := filepath.Join(dir, "custom.onnx")
	labels := filepath.Join(dir, "custom_labels.txt")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir())

	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2,
		modelFileSet{model: model, labels: labels}, false)

	assert.False(t, usedFallback, "a present configured set must not trigger the fallback")
	assert.Equal(t, model, got.model)
	assert.Equal(t, labels, got.labels)
}

// TestResolveFamilyPaths_MissingConfiguredPathFallsBackToInstalled covers the
// GitHub #4201 / #4204 case: settings point at a file that is no longer on disk
// while a gallery variant is installed. Before the fix the model never loaded.
func TestResolveFamilyPaths_MissingConfiguredPathFallsBackToInstalled(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  "/nonexistent/perch_v2.onnx",
		labels: "/nonexistent/perch_v2_labels.txt",
	}, false)

	assert.True(t, usedFallback, "a missing configured path must trigger the fallback")
	assert.Equal(t, installedModel, got.model)
	assert.Equal(t, installedLabels, got.labels)
}

// TestResolveFamilyPaths_PartiallyMissingSetIsReplacedAsAUnit is the regression
// test for the mismatch that per-file resolution produces. With only the labels
// path stale, a per-file fallback keeps the configured model (variant A) and
// takes the labels from whichever variant the gallery probe finds first
// (variant B). That pairing either fails with a label-count mismatch or, when
// two variants happen to declare the same count, silently mislabels every
// detection. The whole set must move together.
func TestResolveFamilyPaths_PartiallyMissingSetIsReplacedAsAUnit(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	// Only the fp32 variant is installed; the configured model path names a
	// different variant's file that still exists on disk.
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	strayDir := t.TempDir()
	strayModel := filepath.Join(strayDir, "perch_v2_other_variant.onnx")
	require.NoError(t, os.WriteFile(strayModel, []byte("m"), 0o600))

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  strayModel,                                    // exists
		labels: filepath.Join(strayDir, "missing_labels.txt"), // does not
	}, false)

	require.True(t, usedFallback)
	assert.Equal(t, installedModel, got.model,
		"the configured model must be discarded with its missing labels, not paired with fallback labels")
	assert.Equal(t, installedLabels, got.labels)
	assert.NotEqual(t, strayModel, got.model,
		"pairing a configured model with fallback labels from another variant mislabels detections")
}

// TestResolveFamilyPaths_NothingInstalledKeepsConfiguredSet asserts that when
// the gallery has nothing to offer, the configured set is returned untouched so
// the caller still reports its "not installed or configured" error. Returning
// an empty set here would also destroy the caller's error context, and a set
// that is only temporarily unreadable (an unmounted volume) must not be
// silently blanked.
func TestResolveFamilyPaths_NothingInstalledKeepsConfiguredSet(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir())

	configured := modelFileSet{
		model:  "/nonexistent/perch_v2.onnx",
		labels: "/nonexistent/perch_v2_labels.txt",
	}
	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, configured, false)

	assert.False(t, usedFallback)
	assert.Equal(t, configured, got)
}

// TestResolveFamilyPaths_BatEmbeddingsParticipateInAtomicity asserts the bat
// family treats its shared embedding extractor as part of the set: a present
// classifier and labels with a missing embedding model must still replace the
// whole set rather than keep two of three configured paths.
func TestResolveFamilyPaths_BatEmbeddingsParticipateInAtomicity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	model := filepath.Join(dir, "bat.onnx")
	labels := filepath.Join(dir, "bat_labels.txt")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir()) // empty gallery: nothing to fall back to

	configured := modelFileSet{
		model:      model,
		labels:     labels,
		embeddings: filepath.Join(dir, "missing_embeddings.onnx"),
	}
	got, usedFallback := o.resolveFamilyPaths(RegistryIDBat, configured, true)

	// No installed bat model, so the configured set comes back untouched, but the
	// point is that allPresentOnDisk rejected it rather than accepting two of three.
	assert.False(t, usedFallback)
	assert.Equal(t, configured, got)
	assert.False(t, configured.allPresentOnDisk(true),
		"a set with a missing embedding model must not count as present")
	assert.True(t, modelFileSet{model: model, labels: labels}.allPresentOnDisk(false),
		"the same two files are a complete set for a family that needs no embeddings")
}

// TestIsGalleryManagedPath gates the automatic config repair: only paths the
// gallery owns may be rewritten, so a user's hand-configured custom model is
// never taken over.
func TestIsGalleryManagedPath(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	modelsDir := t.TempDir()
	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	t.Run("path under the models directory is gallery managed", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(modelsDir, "perch-v2", "anything.onnx")
		assert.True(t, o.isGalleryManagedPath(RegistryIDPerchV2, p))
	})

	t.Run("catalog file name outside the models directory is gallery managed", func(t *testing.T) {
		t.Parallel()
		// This is the shape the issues report: the models directory prefix changed
		// (a different container HOME), so the stale path no longer sits under the
		// current models dir, but the file name is unmistakably ours.
		p := "/home/someone-else/.config/birdnet-go/models/perch-v2/" + galleryName
		assert.True(t, o.isGalleryManagedPath(RegistryIDPerchV2, p))
	})

	t.Run("a user's custom model is not gallery managed", func(t *testing.T) {
		t.Parallel()
		assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, "/srv/my-models/my_own_perch.onnx"))
	})

	t.Run("empty path is not gallery managed", func(t *testing.T) {
		t.Parallel()
		assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, ""))
	})
}

// TestPlanPathCorrection covers the automatic configuration repair. It exercises
// planPathCorrection rather than applyPathCorrection deliberately: the latter
// calls conf.SaveSettings, which would write to the developer's own
// ~/.config/birdnet-go/config.yaml when the test runs outside a container.
func TestPlanPathCorrection(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	newModel := "/models/perch-v2/perch_v2_reunion_no_dft.onnx"
	newLabels := "/models/perch-v2/perch_v2_reunion_labels.txt"

	t.Run("stale gallery path is repaired", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		// A path the gallery wrote, under a models directory prefix that no longer
		// applies: exactly the state a container HOME change leaves behind.
		stalePath := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryName
		current.Perch.ModelPath = stalePath
		current.Perch.LabelPath = "/home/birdnet/.config/birdnet-go/models/perch-v2/perch_v2_labels.txt"

		updated, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
		})

		require.True(t, changed)
		assert.Equal(t, newModel, updated.Perch.ModelPath)
		assert.NotSame(t, current, updated, "the published snapshot must be a clone, never mutated in place")
		assert.Equal(t, stalePath, current.Perch.ModelPath,
			"the input snapshot must be left untouched")
	})

	t.Run("a user's custom path is left alone", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		current.Perch.ModelPath = "/srv/my-models/my_own_perch.onnx"
		current.Perch.LabelPath = "/srv/my-models/my_own_labels.txt"

		_, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
		})

		assert.False(t, changed,
			"a custom model path must never be taken over: it may simply be on a volume that is not mounted yet")
	})

	t.Run("an empty path is not filled in", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{} // both paths empty: the gallery already owns them

		_, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
		})

		assert.False(t, changed,
			"an empty path already resolves through the gallery; writing an absolute path back would only go stale again")
	})

	t.Run("bat repairs all three paths including embeddings", func(t *testing.T) {
		t.Parallel()

		modelsDir := t.TempDir()
		o := &Orchestrator{}
		o.SetModelsDir(modelsDir)

		current := &conf.Settings{}
		current.Bat.ClassifierModel = filepath.Join(modelsDir, "stale_classifier.onnx")
		current.Bat.LabelPath = filepath.Join(modelsDir, "stale_labels.txt")
		current.Bat.EmbeddingModel = filepath.Join(modelsDir, "shared", "stale_embeddings.onnx")

		updated, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDBat,
			resolved: modelFileSet{
				model:      "/models/bat/classifier.onnx",
				labels:     "/models/bat/labels.txt",
				embeddings: "/models/shared/embeddings.onnx",
			},
		})

		require.True(t, changed)
		assert.Equal(t, "/models/bat/classifier.onnx", updated.Bat.ClassifierModel)
		assert.Equal(t, "/models/bat/labels.txt", updated.Bat.LabelPath)
		assert.Equal(t, "/models/shared/embeddings.onnx", updated.Bat.EmbeddingModel,
			"the shared embedding extractor goes stale with the rest of the set")
	})

	t.Run("an unknown registry ID changes nothing", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		current.Perch.ModelPath = "/models/perch-v2/perch_v2.onnx"

		updated, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: "Not_A_Model",
			resolved:   modelFileSet{model: newModel, labels: newLabels},
		})

		assert.False(t, changed)
		assert.Same(t, current, updated)
	})
}

// TestResolveFamilyPaths_KeepsConfiguredVariantWhenOnlyCompanionIsMissing
// asserts that losing a companion file does not silently switch the user to a
// different regional variant. Two variants' model files can coexist on disk
// after a partial cleanup or an interrupted variant switch, and the generic
// gallery probe returns whichever the catalog lists first. That pairing is
// internally consistent, so nothing fails loudly, and the user ends up running a
// model for the wrong region without being told.
func TestResolveFamilyPaths_KeepsConfiguredVariantWhenOnlyCompanionIsMissing(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	require.GreaterOrEqual(t, len(entry.Variants), 2, "this test needs at least two variants")

	// Pick two variants that declare distinct model file names.
	first := entry.Variants[0]
	var second CatalogVariant
	for i := 1; i < len(entry.Variants); i++ {
		if modelRoleLocalName(t, entry.Variants[i].Files) != modelRoleLocalName(t, first.Files) {
			second = entry.Variants[i]
			break
		}
	}
	require.NotEmpty(t, second.ID, "expected a second variant with a distinct model file")

	modelsDir := t.TempDir()
	// Both variants' models are on disk; only the SECOND variant is the one the
	// configuration names, and only its labels file is missing.
	writeVariantModelFile(t, modelsDir, &entry, first.ID)
	writeVariantLabelsFile(t, modelsDir, &entry, first.ID)
	configuredModel := writeVariantModelFile(t, modelsDir, &entry, second.ID)
	expectedLabels := writeVariantLabelsFile(t, modelsDir, &entry, second.ID)

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	got, _ := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  configuredModel,
		labels: filepath.Join(modelsDir, entry.ID, "gone_labels.txt"),
	}, false)

	assert.Equal(t, configuredModel, got.model,
		"the variant the configuration names must be kept when its own files are recoverable")
	assert.Equal(t, expectedLabels, got.labels,
		"companion files must come from the SAME variant, not from whichever the catalog lists first")
}

// TestResolveFamilyPaths_SafeUnderOrchestratorLock guards the lock protocol.
// resolveFamilyPaths runs inside the secondary model loaders, which hold
// o.mu.Lock(). sync.RWMutex is not reentrant, so any read lock taken on that
// path self-deadlocks the whole load: the model never loads, the audio pipeline
// never registers it, and the process hangs at startup with no error. A first
// cut of resolveSiblingSet did exactly that, and the unit tests passed because
// they called it without the lock held.
func TestResolveFamilyPaths_SafeUnderOrchestratorLock(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Exactly how the loaders call it.
		o.mu.Lock()
		defer o.mu.Unlock()

		// Fallback path: configured file is absent.
		_, _ = o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)

		// Sibling-recovery path: configured model present, companion missing.
		_, _ = o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  installedModel,
			labels: filepath.Join(modelsDir, entry.ID, "gone_labels.txt"),
		}, false)

		// Queueing a correction is also done under the lock.
		o.queuePathCorrectionIfFallback(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("resolveFamilyPaths deadlocked while o.mu was held; it must not acquire o.mu")
	}
}
