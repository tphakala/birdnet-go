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

// embeddingsRoleLocalName returns the embeddings file name a catalog entry
// declares.
func embeddingsRoleLocalName(t *testing.T, files []CatalogFile) string {
	t.Helper()
	for _, f := range files {
		if f.Role == RoleEmbeddings {
			return f.LocalName
		}
	}
	t.Fatalf("entry declares no embeddings file")
	return ""
}

// firstBatCatalogEntry returns the first catalog entry whose registry ID is the
// bat family, so a test does not hard-code a regional variant that may change.
func firstBatCatalogEntry(t *testing.T) *CatalogEntry {
	t.Helper()
	catalog := ActiveCatalog()
	for i := range catalog {
		if catalog[i].RegistryID == RegistryIDBat {
			return &catalog[i]
		}
	}
	t.Fatalf("no bat catalog entry found")
	return nil
}

// writeBatGalleryFiles writes a flat bat catalog entry's model and labels into
// its per-model subdirectory and its shared embedding extractor into
// models/shared/, mimicking an installed bat model in the gallery layout, and
// returns the three paths.
func writeBatGalleryFiles(t *testing.T, modelsDir string, entry *CatalogEntry) (model, labels, embeddings string) {
	t.Helper()
	subdir := filepath.Join(modelsDir, entry.ID)
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	for i := range entry.Files {
		f := &entry.Files[i]
		switch f.Role {
		case RoleModel:
			model = filepath.Join(subdir, f.LocalName)
			require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
		case RoleLabels:
			labels = filepath.Join(subdir, f.LocalName)
			require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))
		case RoleEmbeddings:
			embeddings = filepath.Join(sharedDir, f.LocalName)
			require.NoError(t, os.WriteFile(embeddings, []byte("e"), 0o600))
		}
	}
	require.NotEmpty(t, model, "bat entry declares a model file")
	require.NotEmpty(t, labels, "bat entry declares a labels file")
	require.NotEmpty(t, embeddings, "bat entry declares an embeddings file")
	return model, labels, embeddings
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

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	// A real installed gallery variant whose paths DIFFER from the configured
	// custom set. Without an installed variant a wrong fallback would return the
	// same (unchanged) configured set and the assertion could not tell the two
	// apart; with one installed, a wrong fallback returns these gallery paths.
	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	dir := t.TempDir()
	model := filepath.Join(dir, "custom.onnx")
	labels := filepath.Join(dir, "custom_labels.txt")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2,
		modelFileSet{model: model, labels: labels}, false)

	assert.False(t, usedFallback, "a present configured set must not trigger the fallback")
	assert.Equal(t, model, got.model)
	assert.Equal(t, labels, got.labels)
	assert.NotEqual(t, installedModel, got.model,
		"the configured custom model must be used verbatim, not the installed gallery variant")
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

// TestResolveFamilyPaths_PartiallyMissingSetIsReplacedAsAUnit checks that when a
// non-catalog configured model is present but its labels are missing, the whole
// set is discarded for the installed gallery set rather than pairing the
// configured model with fallback labels (which would either fail with a
// label-count mismatch or silently mislabel every detection).
//
// The configured model here is a STRAY file whose basename matches no catalog
// variant, so resolveSiblingSet finds no match and the generic gallery probe
// supplies the whole replacement set. The cross-variant recovery path (where the
// configured model IS a catalog variant and only a companion is missing) is
// covered separately by TestResolveFamilyPaths_KeepsConfiguredVariantWhenOnlyCompanionIsMissing.
func TestResolveFamilyPaths_PartiallyMissingSetIsReplacedAsAUnit(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	// Only the fp32 variant is installed.
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	strayDir := t.TempDir()
	// A file name that matches no catalog variant, so this exercises the
	// unknown-file branch (no sibling recovery), not the cross-variant branch.
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

	batEntry := firstBatCatalogEntry(t)

	modelsDir := t.TempDir()
	// A real installed bat gallery variant so the fallback is observable: without
	// it the missing embedding could not be distinguished from a set kept as-is.
	galleryModel, galleryLabels, galleryEmb := writeBatGalleryFiles(t, modelsDir, batEntry)

	dir := t.TempDir()
	model := filepath.Join(dir, "bat.onnx")
	labels := filepath.Join(dir, "bat_labels.txt")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// Present classifier and labels, but a missing (non-empty) embedding path: the
	// set is stale and must be replaced WHOLE by the installed gallery set, not
	// left pairing two configured paths with a fallback embedding.
	configured := modelFileSet{
		model:      model,
		labels:     labels,
		embeddings: filepath.Join(dir, "missing_embeddings.onnx"),
	}
	got, usedFallback := o.resolveFamilyPaths(RegistryIDBat, configured, true)

	require.True(t, usedFallback, "a missing embedding member makes the whole set stale")
	assert.Equal(t, galleryModel, got.model,
		"the classifier moves with the set; it must not be kept while the embeddings are replaced")
	assert.Equal(t, galleryLabels, got.labels)
	assert.Equal(t, galleryEmb, got.embeddings)
	assert.NotEqual(t, model, got.model,
		"keeping the configured classifier while replacing the embeddings would mismatch the set")

	// The invariants the atomicity rests on.
	assert.False(t, configured.allPresentOnDisk(true),
		"a set with a missing embedding model must not count as present")
	assert.True(t, modelFileSet{model: model, labels: labels}.allPresentOnDisk(false),
		"the same two files are a complete set for a family that needs no embeddings")
}

// TestResolveFamilyPaths_EmptyMemberFilledMissingMemberReplaced pins A1's rule:
// an EMPTY configured member is filled from the gallery with no repair (it is the
// supported gallery-owns-it convention), whereas a MISSING non-empty member makes
// the whole set stale and triggers the fallback + repair.
func TestResolveFamilyPaths_EmptyMemberFilledMissingMemberReplaced(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	t.Run("empty member is filled from the gallery without a repair", func(t *testing.T) {
		t.Parallel()
		got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  installedModel, // present
			labels: "",             // empty: the gallery owns it
		}, false)
		assert.False(t, usedFallback, "an empty member is not stale, so no repair is queued")
		assert.Equal(t, installedModel, got.model, "the present configured model is kept")
		assert.Equal(t, installedLabels, got.labels, "the empty labels member is filled from the gallery")
	})

	t.Run("a missing non-empty member replaces the set with a repair", func(t *testing.T) {
		t.Parallel()
		got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)
		assert.True(t, usedFallback, "a missing non-empty member is stale and queues a repair")
		assert.Equal(t, installedModel, got.model)
	})
}

// TestResolveFamilyPaths_IndeterminateFallsBackWithoutRepair pins A2's third
// outcome: a configured path that is unreadable for a reason OTHER than absence
// (here ENOTDIR, standing in for a volume that has not finished mounting) still
// falls back at runtime so analysis can run, but must NOT rewrite config.
func TestResolveFamilyPaths_IndeterminateFallsBackWithoutRepair(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	// A regular file used as a path prefix makes os.Stat return ENOTDIR, which is
	// indeterminate (not a confirmed absence).
	dir := t.TempDir()
	regular := filepath.Join(dir, "not-a-dir")
	require.NoError(t, os.WriteFile(regular, []byte("x"), 0o600))
	indeterminate := modelFileSet{
		model:  filepath.Join(regular, "perch_v2.onnx"),
		labels: filepath.Join(regular, "perch_v2_labels.txt"),
	}

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, indeterminate, false)

	assert.False(t, usedFallback,
		"an unreadable path is indeterminate: fall back at runtime but do NOT rewrite config")
	assert.Equal(t, installedModel, got.model,
		"the model still resolves to the installed gallery model so analysis can run")
	assert.Equal(t, installedLabels, got.labels)

	o.queuePathCorrectionIfFallback(RegistryIDPerchV2, indeterminate, false)
	assert.Empty(t, o.pendingPathCorrections,
		"an indeterminate path must not queue a config repair")
}

// TestQueuePathCorrectionIfFallback_HealthySetQueuesNothing is the negative for
// the self-heal: a fully present configured set must not queue any repair.
func TestQueuePathCorrectionIfFallback_HealthySetQueuesNothing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	model := filepath.Join(dir, "custom.onnx")
	labels := filepath.Join(dir, "custom_labels.txt")
	require.NoError(t, os.WriteFile(model, []byte("m"), 0o600))
	require.NoError(t, os.WriteFile(labels, []byte("l"), 0o600))

	// A real gallery variant must be installed, otherwise this test cannot fail:
	// with an empty models directory the fallback also returns the configured set
	// and queues nothing, so removing the healthy-set early return entirely would
	// still leave the assertion below green.
	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	modelsDir := t.TempDir()
	writeVariantModelFile(t, modelsDir, &entry, "fp32")
	writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	configured := modelFileSet{model: model, labels: labels}

	// Assert the resolution itself, not only the queue. Queueing is keyed on a
	// CONFIRMED-missing member, so a healthy set queues nothing down several
	// different code paths; without this the test cannot tell the healthy-set
	// early return from a fallback that happened to keep repairable false.
	got, usedFallback := o.resolveFamilyPaths(RegistryIDPerchV2, configured, false)
	assert.False(t, usedFallback)
	assert.Equal(t, configured, got,
		"a healthy configured set must be returned verbatim, never swapped for the installed gallery variant")

	o.queuePathCorrectionIfFallback(RegistryIDPerchV2, configured, false)

	assert.Empty(t, o.pendingPathCorrections,
		"a healthy, fully-present configured set must not queue a config repair")
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

	t.Run("catalog file in the entry-ID subdir under the models directory is gallery managed", func(t *testing.T) {
		t.Parallel()
		p := filepath.Join(modelsDir, "perch-v2", galleryName)
		assert.True(t, o.isGalleryManagedPath(RegistryIDPerchV2, p))
	})

	t.Run("catalog file name in an entry-ID directory outside the models directory is gallery managed", func(t *testing.T) {
		t.Parallel()
		// This is the shape the issues report: the models directory prefix changed
		// (a different container HOME), so the stale path no longer sits under the
		// current models dir, but the parent directory is still the entry ID and the
		// file name is unmistakably ours.
		p := "/home/someone-else/.config/birdnet-go/models/perch-v2/" + galleryName
		assert.True(t, o.isGalleryManagedPath(RegistryIDPerchV2, p))
	})

	t.Run("a catalog file name in a non-entry directory is NOT gallery managed", func(t *testing.T) {
		t.Parallel()
		// The basename is a real catalog file name, but the parent directory is
		// "models", not the entry ID, so this is the user's own copy and must not be
		// taken over. This is the false match the tightened rule closes.
		assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, "/mnt/nas/models/"+galleryName))
	})

	t.Run("a non-catalog file name under the entry-ID subdir is NOT gallery managed", func(t *testing.T) {
		t.Parallel()
		// Correct parent directory but a basename the catalog never declares: a
		// user's custom model dropped into the gallery subdir is still theirs.
		p := filepath.Join(modelsDir, "perch-v2", "anything.onnx")
		assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, p))
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
	galleryLabelsName := labelsRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	newModel := "/models/perch-v2/perch_v2_reunion_no_dft.onnx"
	newLabels := "/models/perch-v2/perch_v2_reunion_labels.txt"

	t.Run("stale gallery path is repaired", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		// Paths the gallery wrote, under a models directory prefix that no longer
		// applies: exactly the state a container HOME change leaves behind. Both use
		// real catalog file names so the tightened isGalleryManagedPath recognises
		// them.
		stalePath := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryName
		staleLabels := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryLabelsName
		current.Perch.ModelPath = stalePath
		current.Perch.LabelPath = staleLabels

		updated, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
		})

		require.True(t, changed)
		assert.Equal(t, newModel, updated.Perch.ModelPath)
		assert.Equal(t, newLabels, updated.Perch.LabelPath,
			"the stale labels path must be repaired too, not only the model path")
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

		batEntry := firstBatCatalogEntry(t)

		modelsDir := t.TempDir()
		o := &Orchestrator{}
		o.SetModelsDir(modelsDir)

		// The stale paths must be gallery-managed for the repair to fire: model and
		// labels live in <modelsDir>/<entry ID>/ under their catalog file names, the
		// shared embedding extractor in <modelsDir>/shared/. The tightened
		// isGalleryManagedPath rule (parent directory base name plus a declared
		// LocalName) recognises exactly this layout.
		current := &conf.Settings{}
		current.Bat.ClassifierModel = filepath.Join(modelsDir, batEntry.ID, modelRoleLocalName(t, batEntry.Files))
		current.Bat.LabelPath = filepath.Join(modelsDir, batEntry.ID, labelsRoleLocalName(t, batEntry.Files))
		current.Bat.EmbeddingModel = filepath.Join(modelsDir, "shared", embeddingsRoleLocalName(t, batEntry.Files))

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

	t.Run("birdnet v3 repairs model and labels", func(t *testing.T) {
		t.Parallel()

		v3, okV3 := GetCatalogEntry("birdnet-v3.0")
		require.True(t, okV3)
		v3Model := modelRoleLocalName(t, variantByID(t, &v3, "fp32").Files)
		v3Labels := labelsRoleLocalName(t, variantByID(t, &v3, "fp32").Files)

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		current.BirdNETV3.ModelPath = "/home/birdnet/.config/birdnet-go/models/birdnet-v3.0/" + v3Model
		current.BirdNETV3.LabelPath = "/home/birdnet/.config/birdnet-go/models/birdnet-v3.0/" + v3Labels

		newV3Model := "/models/birdnet-v3.0/other.onnx"
		newV3Labels := "/models/birdnet-v3.0/other_labels.txt"
		updated, changed := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDBirdNETV3,
			resolved:   modelFileSet{model: newV3Model, labels: newV3Labels},
		})

		require.True(t, changed)
		assert.Equal(t, newV3Model, updated.BirdNETV3.ModelPath)
		assert.Equal(t, newV3Labels, updated.BirdNETV3.LabelPath)
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

// TestApplyPathCorrection_PersistsRepairedPaths exercises applyPathCorrection end
// to end (StoreSettings + SaveSettings). It pins that StoreSettings runs BEFORE
// SaveSettings: SaveSettings persists whatever StoreSettings last published, so
// reversing them would write the stale snapshot and the file would still hold the
// old paths. redirectConfigFile points conf.SaveSettings at a temp file so the
// developer's own ~/.config/birdnet-go/config.yaml is never touched.
func TestApplyPathCorrection_PersistsRepairedPaths(t *testing.T) {
	// Not parallel: mutates the conf global settings and conf.ConfigPath.
	redirectConfigFile(t)

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)
	galleryLabelsName := labelsRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	modelsDir := t.TempDir()
	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// Stale gallery-managed paths published as the current settings.
	stale := &conf.Settings{}
	stalePath := filepath.Join(modelsDir, "perch-v2", galleryName)
	stale.Perch.ModelPath = stalePath
	stale.Perch.LabelPath = filepath.Join(modelsDir, "perch-v2", galleryLabelsName)
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	newModel := "/models/perch-v2/perch_v2_reunion_no_dft.onnx"
	newLabels := "/models/perch-v2/perch_v2_reunion_labels.txt"
	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   modelFileSet{model: newModel, labels: newLabels},
	})

	// The published snapshot carries the repaired paths.
	got := conf.GetSettings()
	require.NotNil(t, got)
	assert.Equal(t, newModel, got.Perch.ModelPath)
	assert.Equal(t, newLabels, got.Perch.LabelPath)

	// And so does the persisted file. If SaveSettings ran BEFORE StoreSettings it
	// would have persisted the stale snapshot instead.
	data, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), newModel,
		"the persisted config must carry the repaired path; StoreSettings must run before SaveSettings")
	assert.NotContains(t, string(data), stalePath,
		"the stale model path must not survive in the persisted config")
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
