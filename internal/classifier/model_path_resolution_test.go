package classifier

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
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

	res := o.resolveFamilyPaths(RegistryIDPerchV2,
		modelFileSet{model: model, labels: labels}, false)

	assert.False(t, res.substituted, "a present configured set must not be substituted")
	assert.False(t, res.repairable, "and nothing may be rewritten")
	assert.Equal(t, model, res.resolved.model)
	assert.Equal(t, labels, res.resolved.labels)
	assert.NotEqual(t, installedModel, res.resolved.model,
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

	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  "/nonexistent/perch_v2.onnx",
		labels: "/nonexistent/perch_v2_labels.txt",
	}, false)

	assert.True(t, res.substituted, "a missing configured path must substitute the installed set")
	assert.True(t, res.repairable, "a CONFIRMED absence may also rewrite config")
	assert.Equal(t, installedModel, res.resolved.model)
	assert.Equal(t, installedLabels, res.resolved.labels)
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

	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  strayModel,                                    // exists
		labels: filepath.Join(strayDir, "missing_labels.txt"), // does not
	}, false)

	require.True(t, res.substituted)
	require.True(t, res.repairable)
	assert.Equal(t, installedModel, res.resolved.model,
		"the configured model must be discarded with its missing labels, not paired with fallback labels")
	assert.Equal(t, installedLabels, res.resolved.labels)
	assert.NotEqual(t, strayModel, res.resolved.model,
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
	res := o.resolveFamilyPaths(RegistryIDPerchV2, configured, false)

	assert.False(t, res.substituted,
		"nothing was installed to substitute, so the user is still running what they configured")
	assert.False(t, res.repairable)
	assert.Equal(t, configured, res.resolved)
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
	res := o.resolveFamilyPaths(RegistryIDBat, configured, true)

	require.True(t, res.substituted, "a missing embedding member substitutes the whole set")
	require.True(t, res.repairable, "a missing embedding member makes the whole set stale")
	assert.Equal(t, galleryModel, res.resolved.model,
		"the classifier moves with the set; it must not be kept while the embeddings are replaced")
	assert.Equal(t, galleryLabels, res.resolved.labels)
	assert.Equal(t, galleryEmb, res.resolved.embeddings)
	assert.NotEqual(t, model, res.resolved.model,
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
		res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  installedModel, // present
			labels: "",             // empty: the gallery owns it
		}, false)
		assert.False(t, res.substituted,
			"filling an EMPTY member from the gallery is not a substitution: nothing the user chose was replaced")
		assert.False(t, res.repairable, "an empty member is not stale, so no repair is queued")
		assert.Equal(t, installedModel, res.resolved.model, "the present configured model is kept")
		assert.Equal(t, installedLabels, res.resolved.labels, "the empty labels member is filled from the gallery")
	})

	t.Run("a missing non-empty member replaces the set with a repair", func(t *testing.T) {
		t.Parallel()
		res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)
		assert.True(t, res.substituted, "a missing non-empty member replaces what the user chose")
		assert.True(t, res.repairable, "a missing non-empty member is stale and queues a repair")
		assert.Equal(t, installedModel, res.resolved.model)
	})
}

// TestResolveFamilyPaths_EmptyMemberFilledFromConfiguredVariant asserts that when
// a configured model names a REGIONAL variant present on disk and its labels
// member is empty, the empty labels are filled from that SAME variant, not from a
// different installed variant (a global build) that the catalog happens to list
// first. For bat, whose label files are per-region with region-specific counts, a
// count coincidence would otherwise silently mislabel every detection.
func TestResolveFamilyPaths_EmptyMemberFilledFromConfiguredVariant(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := t.TempDir()
	// A global build is installed and is listed FIRST in the catalog, so
	// resolveInstalledPaths returns its labels (perch_v2_labels.txt).
	writeVariantModelFile(t, modelsDir, &entry, "fp32")
	globalLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	// The configuration names a regional variant, also installed, whose labels
	// file is region-specific and distinct from the global one.
	const regionalVariant = "no-dft-fp32@reunion"
	regionalModel := writeVariantModelFile(t, modelsDir, &entry, regionalVariant)
	regionalLabels := writeVariantLabelsFile(t, modelsDir, &entry, regionalVariant)
	require.NotEqual(t, globalLabels, regionalLabels,
		"the two variants must declare distinct labels files for this test to mean anything")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// Configured model present (regional), labels empty: presenceComplete, so the
	// empty labels member is filled. It must be filled from the regional model's
	// own variant.
	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  regionalModel,
		labels: "",
	}, false)

	assert.False(t, res.substituted, "an empty member is filled, not substituted")
	assert.False(t, res.repairable, "an empty member is filled, not a stale fallback")
	assert.Equal(t, regionalModel, res.resolved.model, "the configured regional model is kept")
	assert.Equal(t, regionalLabels, res.resolved.labels,
		"empty labels must be filled from the SAME variant the configured model names")
	assert.NotEqual(t, globalLabels, res.resolved.labels,
		"filling from the catalog-first global variant would mislabel the regional model")
}

// TestResolveFamilyPaths_IndeterminateFallsBackWithoutRepair pins A2's third
// outcome: a configured path that is unreadable for a reason OTHER than absence
// (here ENOTDIR, standing in for a volume that has not finished mounting) still
// falls back at runtime so analysis can run, but must NOT rewrite config.
func TestResolveFamilyPaths_IndeterminateFallsBackWithoutRepair(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		// This test provokes the indeterminate branch with a path whose intermediate
		// component is a regular file, which yields ENOTDIR on Linux and macOS. On
		// Windows the same stat reports ERROR_PATH_NOT_FOUND, which Go maps to
		// fs.ErrNotExist, so nonEmptyMembersPresence returns presenceMissing rather
		// than presenceIndeterminate: repairable becomes true and both assertions
		// below would fail. os.Chmod is not an alternative (it is a no-op on Windows,
		// which would make the test pass vacuously).
		t.Skip("ENOTDIR-via-file-prefix is ERROR_PATH_NOT_FOUND on Windows, mapped to fs.ErrNotExist")
	}

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

	res := o.resolveFamilyPaths(RegistryIDPerchV2, indeterminate, false)

	assert.False(t, res.repairable,
		"an unreadable path is indeterminate: fall back at runtime but do NOT rewrite config")
	assert.Equal(t, installedModel, res.resolved.model,
		"the model still resolves to the installed gallery model so analysis can run")
	assert.Equal(t, installedLabels, res.resolved.labels)

	// The user IS running a model they did not choose, so the substitution has to
	// be reported even though the configuration must not be rewritten. Before
	// these two flags were separated, an indeterminate resolution queued nothing,
	// so applyPathCorrection was never reached and neither notification fired: the
	// only trace was a single Debug line, and a user whose model sat on a NAS
	// mount that started returning EACCES ran a different model indefinitely.
	assert.True(t, res.substituted,
		"an unreadable configured path still substitutes a different model, and that must not be silent")

	o.queuePathCorrection(RegistryIDPerchV2, res)
	assert.Len(t, o.pendingPathCorrections, 1,
		"the substitution must be queued so the drain can notify, even though it may not repair")
	assert.False(t, o.pendingPathCorrections[0].repairable,
		"the queued correction must carry the do-not-rewrite verdict to the drain")
}

// TestApplyPathCorrection_UnreadableNotifiesWithoutRewriting is the other half of
// the indeterminate case: the drain must tell the user AND leave config.yaml
// byte-for-byte as they wrote it. Rewriting here would make a transient
// permissions or mount failure permanent, and staying silent is the failure that
// cost a reporter days of lost detections.
func TestApplyPathCorrection_UnreadableNotifiesWithoutRewriting(t *testing.T) {
	// Not parallel: mutates the conf global settings, conf.ConfigPath, the
	// process-level persistence switch and the global notification service.
	redirectConfigFile(t)

	SetPathCorrectionPersistenceDisabled(false)
	t.Cleanup(func() { SetPathCorrectionPersistenceDisabled(false) })

	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := filepath.Join(t.TempDir(), "models")
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// A gallery-managed configured path, so the ONLY thing standing between this
	// and a rewrite is the repairable verdict. Using a user-owned path instead
	// would let the pre-existing user-owned veto carry the test and the new guard
	// could be deleted with the suite still green.
	staleDir := filepath.Join(t.TempDir(), "models", entry.ID)
	stale := &conf.Settings{}
	stale.Perch.ModelPath = filepath.Join(staleDir, filepath.Base(installedModel))
	stale.Perch.LabelPath = filepath.Join(staleDir, filepath.Base(installedLabels))
	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	before, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)

	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   modelFileSet{model: installedModel, labels: installedLabels},
		repairable: false,
		// Stated explicitly: "not repairable" alone no longer selects the
		// could-not-be-read wording, because the primary also declines to rewrite a
		// path whose written form differs from its expanded form, and that file is
		// absent rather than unreadable.
		unreadable: true,
	})

	after, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"an unreadable configured path must never be persisted over, or a transient failure becomes permanent")

	published := conf.GetSettings()
	assert.Equal(t, stale.Perch.ModelPath, published.Perch.ModelPath,
		"the published snapshot must keep the user's configured path too")

	list, err := svc.List(nil)
	require.NoError(t, err)
	var unreadable, reconciled bool
	for _, n := range list {
		switch n.TitleKey {
		case notification.MsgModelPathUnreadableTitle:
			unreadable = true
		case notification.MsgModelPathReconciledTitle:
			reconciled = true
		}
	}
	assert.True(t, unreadable,
		"the user must be told they are running a substitute; telling them it was 'not found' would send them looking in the wrong place")
	assert.False(t, reconciled,
		"nothing was repaired, so the reconciled notification must not fire")
}

// TestQueuePathCorrection_HealthySetQueuesNothing is the negative for
// the self-heal: a fully present configured set must not queue any repair.
func TestQueuePathCorrection_HealthySetQueuesNothing(t *testing.T) {
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
	res := o.resolveFamilyPaths(RegistryIDPerchV2, configured, false)
	assert.False(t, res.repairable)
	assert.Equal(t, configured, res.resolved,
		"a healthy configured set must be returned verbatim, never swapped for the installed gallery variant")

	o.queuePathCorrection(RegistryIDPerchV2, res)

	assert.Empty(t, o.pendingPathCorrections,
		"a healthy, fully-present configured set must not queue a config repair")
}

// TestPathCorrection_QueuedThenDrainedRepairsConfig is the POSITIVE test for the
// self-heal. It covers the middle of the chain that the two negative tests above
// and TestApplyPathCorrection_PersistsRepairedPaths leave uncovered: that a stale
// configured set is actually QUEUED, and that draining the queue actually
// APPLIES the repair.
//
// Without it the entire self-heal can be deleted in silence. Two mutations leave
// the rest of the suite green: reducing queuePathCorrection to a no-op, and
// reducing runPendingPathCorrections to snapshot-and-discard. Models would still
// load either way, because the runtime fallback is covered separately, so nothing
// would look broken; but config.yaml would never be repaired, the stale basename
// would keep feeding installedModelBasenameHint, and affected users would fall
// back again on every restart forever.
//
// The scenario is the one GitHub #4201 and #4204 report: the models directory
// prefix changed (a different container HOME), so the configured paths still have
// the gallery's <models dir>/<entry ID>/<local name> shape but point under the OLD
// root and no longer exist.
func TestPathCorrection_QueuedThenDrainedRepairsConfig(t *testing.T) {
	// Not parallel: mutates the conf global settings, conf.ConfigPath, the
	// process-level persistence switch, and the global notification service.
	redirectConfigFile(t)

	// A diagnostic command may have disabled persistence process-wide; this test
	// asserts the persisting path, so pin the switch for its duration.
	SetPathCorrectionPersistenceDisabled(false)
	t.Cleanup(func() { SetPathCorrectionPersistenceDisabled(false) })

	// The reconciled notification is the user's only signal that a repair
	// happened, and it had no positive coverage (only the user-owned SUBSTITUTED
	// case was asserted, in TestApplyPathCorrection_UserOwnedSubstitutionNotifies).
	//
	// This assertion pins the emitter, not the whole delivery pipeline.
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	// Name the directory "models" so filepath.Base matches production. That base
	// name is what isGalleryManagedPath compares against, so it lets the stale
	// path below sit under a DIFFERENT root and still qualify as gallery-managed,
	// which is the whole point of the changed-HOME case.
	modelsDir := filepath.Join(t.TempDir(), "models")
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// The same gallery layout under the OLD root: right shape, absent on disk.
	staleDir := filepath.Join(t.TempDir(), "models", entry.ID)
	configured := modelFileSet{
		model:  filepath.Join(staleDir, filepath.Base(installedModel)),
		labels: filepath.Join(staleDir, filepath.Base(installedLabels)),
	}

	stale := &conf.Settings{}
	stale.Perch.ModelPath = configured.model
	stale.Perch.LabelPath = configured.labels
	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	// 1. The resolution reports a repairable fallback onto the installed variant.
	res := o.resolveFamilyPaths(RegistryIDPerchV2, configured, false)
	require.True(t, res.substituted, "a confirmed-missing gallery path substitutes the installed set")
	require.True(t, res.repairable,
		"a confirmed-missing gallery path must be reported as repairable")
	require.Equal(t, installedModel, res.resolved.model)
	require.Equal(t, installedLabels, res.resolved.labels)

	// 2. Queueing records it. A queuePathCorrection that never queues fails here.
	o.queuePathCorrection(RegistryIDPerchV2, res)
	require.Len(t, o.pendingPathCorrections, 1,
		"a repairable fallback must queue exactly one correction")
	assert.Equal(t, RegistryIDPerchV2, o.pendingPathCorrections[0].registryID)
	assert.Equal(t, res.resolved, o.pendingPathCorrections[0].resolved,
		"the queued correction must carry the set the model was actually built from")

	// 3. Draining applies it. A runPendingPathCorrections that snapshots and
	// discards fails here.
	o.runPendingPathCorrections()

	assert.Empty(t, o.pendingPathCorrections,
		"the drain must clear the queue so a later drain cannot rewrite config again")

	got := conf.GetSettings()
	require.NotNil(t, got)
	assert.Equal(t, installedModel, got.Perch.ModelPath,
		"the drain must repair the stale model path in the published snapshot")
	assert.Equal(t, installedLabels, got.Perch.LabelPath,
		"the labels path is repaired as part of the same atomic set")

	data, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), installedModel,
		"the repair must reach config.yaml, otherwise the fallback repeats on every restart")
	assert.NotContains(t, string(data), configured.model,
		"the stale path must not survive in the persisted config")

	// 4. The repair is announced. A silent repair leaves someone who lost
	// detections for days with no explanation, which is the whole reason
	// emitPathReconciledNotification exists.
	list, err := svc.List(nil)
	require.NoError(t, err)
	var reconciled, substituted bool
	for _, n := range list {
		switch n.TitleKey {
		case notification.MsgModelPathReconciledTitle:
			reconciled = true
		case notification.MsgModelPathSubstitutedTitle:
			substituted = true
		}
	}
	assert.True(t, reconciled,
		"a persisted repair must emit the reconciled notification")
	assert.False(t, substituted,
		"config WAS rewritten, so the substituted notification must not fire")
}

// TestRunPendingPathCorrections_DrainsEveryQueuedFamily pins that the drain
// applies EVERY queued correction, not only the first. The Bat family is the
// second correction on purpose: its three-path set (classifier, labels, shared
// embedding extractor) is otherwise never exercised through queue+drain. A drain
// that stops after pending[0] would repair the first family and leave the second
// family's stale config untouched, which is invisible to the rest of the suite.
func TestRunPendingPathCorrections_DrainsEveryQueuedFamily(t *testing.T) {
	// Not parallel: mutates the conf global settings, conf.ConfigPath, and the
	// process-level persistence switch.
	redirectConfigFile(t)

	SetPathCorrectionPersistenceDisabled(false)
	t.Cleanup(func() { SetPathCorrectionPersistenceDisabled(false) })

	perchEntry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	batEntry := firstBatCatalogEntry(t)

	// Installed gallery variants under a "models" root, so the resolved sets point
	// at real, distinct files. Name the directory "models" so filepath.Base matches
	// production and the stale gallery-managed paths below qualify.
	modelsDir := filepath.Join(t.TempDir(), "models")
	installedPerchModel := writeVariantModelFile(t, modelsDir, &perchEntry, "fp32")
	installedPerchLabels := writeVariantLabelsFile(t, modelsDir, &perchEntry, "fp32")
	batModel, batLabels, batEmb := writeBatGalleryFiles(t, modelsDir, batEntry)

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// Stale, gallery-managed configured paths for BOTH families: the same
	// <models>/<entry ID>/<local name> (and <models>/shared/<local name>) shape,
	// but under an OLD root that no longer exists (the changed-container-HOME case).
	// isGalleryManagedPath qualifies them (grandparent base is "models"), and they
	// differ from the resolved sets, so each family's correction is a real change.
	staleRoot := filepath.Join(t.TempDir(), "models")
	stale := &conf.Settings{}
	stale.Perch.ModelPath = filepath.Join(staleRoot, perchEntry.ID, filepath.Base(installedPerchModel))
	stale.Perch.LabelPath = filepath.Join(staleRoot, perchEntry.ID, filepath.Base(installedPerchLabels))
	stale.Bat.ClassifierModel = filepath.Join(staleRoot, batEntry.ID, filepath.Base(batModel))
	stale.Bat.LabelPath = filepath.Join(staleRoot, batEntry.ID, filepath.Base(batLabels))
	stale.Bat.EmbeddingModel = filepath.Join(staleRoot, "shared", filepath.Base(batEmb))

	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	// Queue Perch first, Bat second. A drain that applies only pending[0] would
	// repair Perch and leave the whole Bat set stale.
	o.queuePathCorrection(RegistryIDPerchV2, pathResolution{
		resolved:    modelFileSet{model: installedPerchModel, labels: installedPerchLabels},
		substituted: true,
		repairable:  true,
	})
	o.queuePathCorrection(RegistryIDBat, pathResolution{
		resolved:    modelFileSet{model: batModel, labels: batLabels, embeddings: batEmb},
		substituted: true,
		repairable:  true,
	})
	require.Len(t, o.pendingPathCorrections, 2, "both families must be queued")

	o.runPendingPathCorrections()

	assert.Empty(t, o.pendingPathCorrections, "the drain must clear the queue")

	got := conf.GetSettings()
	require.NotNil(t, got)

	// The first queued family (pending[0]) is repaired.
	assert.Equal(t, installedPerchModel, got.Perch.ModelPath)
	assert.Equal(t, installedPerchLabels, got.Perch.LabelPath)

	// The second queued family (pending[1]) must be repaired by the SAME drain,
	// all three paths. A drain that stops after pending[0] fails these.
	assert.Equal(t, batModel, got.Bat.ClassifierModel,
		"the second queued family must be repaired by the same drain, not skipped")
	assert.Equal(t, batLabels, got.Bat.LabelPath)
	assert.Equal(t, batEmb, got.Bat.EmbeddingModel,
		"the shared embedding extractor is part of the second family's set")
}

// TestIsGalleryManagedPath gates the automatic config repair: only paths the
// gallery owns may be rewritten, so a user's hand-configured custom model is
// never taken over.
func TestIsGalleryManagedPath(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	// The models directory's base name is "models", as it is in production
	// (~/.config/birdnet-go/models). isGalleryManagedPath now requires a candidate
	// path's grandparent directory to match this base name.
	modelsDir := filepath.Join(t.TempDir(), "models")
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

	t.Run("a NAS-mirror path that only mimics the gallery layout is NOT gallery managed", func(t *testing.T) {
		t.Parallel()
		// Correct basename and entry-ID parent, but the grandparent is "nas", not the
		// models directory. This is the "moved my models to a NAS but left the local
		// copy" migration: a late mount yields ENOENT (presenceMissing), and without
		// the grandparent check the user's NAS path would be permanently rewritten to
		// the local gallery copy.
		assert.False(t, o.isGalleryManagedPath(RegistryIDPerchV2, "/mnt/nas/perch-v2/"+galleryName))
	})

	t.Run("a shared-role file under the models directory is gallery managed", func(t *testing.T) {
		t.Parallel()
		// The shared embedding extractor lives in <models>/shared/, so its grandparent
		// is the models directory and it still qualifies.
		batEntry := firstBatCatalogEntry(t)
		embName := embeddingsRoleLocalName(t, batEntry.Files)
		p := filepath.Join(modelsDir, "shared", embName)
		assert.True(t, o.isGalleryManagedPath(RegistryIDBat, p))
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
		// Base name "models" so the changed-HOME stale paths below (grandparent
		// "models") satisfy the grandparent requirement in isGalleryManagedPath.
		o.SetModelsDir(filepath.Join(t.TempDir(), "models"))

		current := &conf.Settings{}
		// Paths the gallery wrote, under a models directory prefix that no longer
		// applies: exactly the state a container HOME change leaves behind. Both use
		// real catalog file names so the tightened isGalleryManagedPath recognises
		// them.
		stalePath := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryName
		staleLabels := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryLabelsName
		current.Perch.ModelPath = stalePath
		current.Perch.LabelPath = staleLabels

		updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
			repairable: true,
		})

		assert.Equal(t, correctionRewrite, outcome)
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

		_, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
			repairable: true,
		})

		assert.Equal(t, correctionSubstituted, outcome,
			"a custom model path must never be taken over: it may simply be on a volume that is not mounted yet")
	})

	t.Run("an empty path is not filled in", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{} // both paths empty: the gallery already owns them

		_, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
			repairable: true,
		})

		assert.Equal(t, correctionNoop, outcome,
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

		updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDBat,
			resolved: modelFileSet{
				model:      "/models/bat/classifier.onnx",
				labels:     "/models/bat/labels.txt",
				embeddings: "/models/shared/embeddings.onnx",
			},
			repairable: true,
		})

		assert.Equal(t, correctionRewrite, outcome)
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
		o.SetModelsDir(filepath.Join(t.TempDir(), "models"))

		current := &conf.Settings{}
		current.BirdNETV3.ModelPath = "/home/birdnet/.config/birdnet-go/models/birdnet-v3.0/" + v3Model
		current.BirdNETV3.LabelPath = "/home/birdnet/.config/birdnet-go/models/birdnet-v3.0/" + v3Labels

		newV3Model := "/models/birdnet-v3.0/other.onnx"
		newV3Labels := "/models/birdnet-v3.0/other_labels.txt"
		updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDBirdNETV3,
			resolved:   modelFileSet{model: newV3Model, labels: newV3Labels},
			repairable: true,
		})

		assert.Equal(t, correctionRewrite, outcome)
		assert.Equal(t, newV3Model, updated.BirdNETV3.ModelPath)
		assert.Equal(t, newV3Labels, updated.BirdNETV3.LabelPath)
	})

	t.Run("an unknown registry ID changes nothing", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(t.TempDir())

		current := &conf.Settings{}
		current.Perch.ModelPath = "/models/perch-v2/perch_v2.onnx"

		updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: "Not_A_Model",
			resolved:   modelFileSet{model: newModel, labels: newLabels},
			repairable: true,
		})

		assert.Equal(t, correctionUnknownFamily, outcome)
		assert.Nil(t, updated,
			"an unknown registry returns nil, never the live published snapshot")
	})

	t.Run("a mixed set with one user-owned field is abandoned whole", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(filepath.Join(t.TempDir(), "models"))

		// The model path is a stale gallery-managed path (would be rewritten on its
		// own); the labels path is the user's own custom file. Both DIFFER from the
		// resolved set. Because a resolved set is atomic, rewriting only the model
		// while keeping the custom labels would persist a cross-variant hybrid, so
		// the whole correction must be abandoned.
		current := &conf.Settings{}
		current.Perch.ModelPath = "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryName
		current.Perch.LabelPath = "/srv/my-models/my_own_labels.txt"

		updated, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: newModel, labels: newLabels},
			repairable: true,
		})

		assert.Equal(t, correctionSubstituted, outcome,
			"a user-owned field must veto the whole correction, not just its own field")
		assert.Nil(t, updated,
			"an abandoned correction returns nil so the gallery-managed model is never written")
	})

	t.Run("a gallery path already equal to the resolved value is not rewritten", func(t *testing.T) {
		t.Parallel()

		o := &Orchestrator{}
		o.SetModelsDir(filepath.Join(t.TempDir(), "models"))

		// Gallery-managed paths that ALREADY equal the resolved set. There is nothing
		// to change: the no-op guard must skip a field whose value already matches,
		// so the plan reports outcome==correctionNoop rather than "rewriting" a field to the
		// value it already holds.
		samePath := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryName
		sameLabels := "/home/birdnet/.config/birdnet-go/models/perch-v2/" + galleryLabelsName
		current := &conf.Settings{}
		current.Perch.ModelPath = samePath
		current.Perch.LabelPath = sameLabels

		_, outcome := o.planPathCorrection(current, &pendingPathCorrection{
			registryID: RegistryIDPerchV2,
			resolved:   modelFileSet{model: samePath, labels: sameLabels},
			repairable: true,
		})

		assert.Equal(t, correctionNoop, outcome,
			"a configured path already equal to the resolved value is not a change")
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
	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	newModel := "/models/perch-v2/perch_v2_reunion_no_dft.onnx"
	newLabels := "/models/perch-v2/perch_v2_reunion_labels.txt"
	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   modelFileSet{model: newModel, labels: newLabels},
		repairable: true,
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

// TestApplyPathCorrection_NoWriteWhenNothingChanged pins the redundant-write
// short-circuit: when planPathCorrection reports no change (the configured paths
// already equal the resolved set), applyPathCorrection must NOT publish a new
// snapshot and must NOT persist config.yaml. Without the `switch outcome`
// guard the whole package stays green while a StoreSettings + SaveSettings runs
// on a plan that changed nothing.
func TestApplyPathCorrection_NoWriteWhenNothingChanged(t *testing.T) {
	// Not parallel: mutates the conf global settings and conf.ConfigPath.
	redirectConfigFile(t)

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryName := modelRoleLocalName(t, variantByID(t, &entry, "fp32").Files)
	galleryLabelsName := labelsRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	modelsDir := t.TempDir()
	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// Gallery-managed paths published as current, and the resolved set is the SAME:
	// there is nothing to repair, so planPathCorrection reports outcome==correctionNoop.
	same := &conf.Settings{}
	same.Perch.ModelPath = filepath.Join(modelsDir, "perch-v2", galleryName)
	same.Perch.LabelPath = filepath.Join(modelsDir, "perch-v2", galleryLabelsName)

	origSettings := conf.GetSettings()
	conf.StoreSettings(same)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	before, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)

	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   modelFileSet{model: same.Perch.ModelPath, labels: same.Perch.LabelPath},
		repairable: true,
	})

	assert.Same(t, same, conf.GetSettings(),
		"nothing changed, so applyPathCorrection must not publish a new snapshot")
	after, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"nothing changed, so applyPathCorrection must not persist config.yaml")
}

// TestApplyPathCorrection_PersistenceDisabledSkipsWrite covers the read-only
// diagnostic path (benchmark, rangefilter print): a stale gallery path still
// resolves via the runtime fallback, but with persistence disabled the correction
// is NOT written to config.yaml and no new snapshot is published.
func TestApplyPathCorrection_PersistenceDisabledSkipsWrite(t *testing.T) {
	// Not parallel: mutates the conf global settings, conf.ConfigPath, and the
	// process-level persistence switch.
	redirectConfigFile(t)

	SetPathCorrectionPersistenceDisabled(true)
	t.Cleanup(func() { SetPathCorrectionPersistenceDisabled(false) })

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	galleryLabelsName := labelsRoleLocalName(t, variantByID(t, &entry, "fp32").Files)

	// An installed gallery variant so the fallback has something to resolve to. Base
	// name "models" so the stale gallery-managed path below satisfies the
	// grandparent requirement in isGalleryManagedPath.
	modelsDir := filepath.Join(t.TempDir(), "models")
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// A stale, gallery-managed configured model (a regional variant's declared file
	// name, so isGalleryManagedPath qualifies it) that is confirmed missing on disk.
	// The labels member already equals the resolved labels, so only the model would
	// be rewritten: planPathCorrection reports a real change on the serve path.
	stale := &conf.Settings{}
	stale.Perch.ModelPath = filepath.Join(modelsDir, "perch-v2", "perch_v2_reunion_no_dft.onnx")
	stale.Perch.LabelPath = filepath.Join(modelsDir, "perch-v2", galleryLabelsName)

	// The runtime fallback still resolves the missing path to the installed model,
	// independent of persistence.
	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  stale.Perch.ModelPath,
		labels: stale.Perch.LabelPath,
	}, false)
	require.True(t, res.repairable, "a confirmed-missing configured path must still fall back")
	assert.Equal(t, installedModel, res.resolved.model,
		"the fallback still resolves the model so analysis can run")
	assert.Equal(t, installedLabels, res.resolved.labels)

	origSettings := conf.GetSettings()
	conf.StoreSettings(stale)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	before, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)

	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved:   res.resolved,
		repairable: true,
	})

	assert.Same(t, stale, conf.GetSettings(),
		"persistence disabled: applyPathCorrection must not publish a new snapshot")
	after, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"persistence disabled: conf.SaveSettings must not run and config.yaml must be unchanged")
}

// TestApplyPathCorrection_UserOwnedSubstitutionNotifies covers the silent-runtime
// -substitution gap: a user's custom model path is confirmed missing, so the
// gallery fallback runs a different model, but the config is (correctly) NOT
// rewritten because the path is user-owned. Without a notification the user runs a
// model they never chose with no signal. applyPathCorrection must emit the
// substituted notification (distinct from the reconciled one) and leave config
// untouched.
func TestApplyPathCorrection_UserOwnedSubstitutionNotifies(t *testing.T) {
	// Not parallel: mutates conf globals, conf.ConfigPath, the persistence switch,
	// and the process-global notification service.
	redirectConfigFile(t)

	SetPathCorrectionPersistenceDisabled(false)

	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	t.Cleanup(svc.Stop)

	modelsDir := filepath.Join(t.TempDir(), "models")
	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// A user's own model path (not gallery-managed): the self-heal must not rewrite
	// it, so planPathCorrection abandons the correction and returns a nil snapshot.
	current := &conf.Settings{}
	current.Perch.ModelPath = "/srv/my-models/my_own_perch.onnx"
	current.Perch.LabelPath = "/srv/my-models/my_own_labels.txt"

	origSettings := conf.GetSettings()
	conf.StoreSettings(current)
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	before, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)

	// The fallback resolved to the installed gallery model, differing from the
	// configured (missing) custom path.
	o.applyPathCorrection(&pendingPathCorrection{
		registryID: RegistryIDPerchV2,
		resolved: modelFileSet{
			model:  filepath.Join(modelsDir, "perch-v2", "perch_v2.onnx"),
			labels: filepath.Join(modelsDir, "perch-v2", "perch_v2_labels.txt"),
		},
		repairable: true,
	})

	list, err := svc.List(nil)
	require.NoError(t, err)
	var substituted, reconciled bool
	for _, n := range list {
		switch n.TitleKey {
		case notification.MsgModelPathSubstitutedTitle:
			substituted = true
		case notification.MsgModelPathReconciledTitle:
			reconciled = true
		}
	}
	assert.True(t, substituted,
		"a user-owned path substituted at runtime must produce the substituted notification")
	assert.False(t, reconciled,
		"config was not rewritten, so the reconciled notification must NOT fire")

	assert.Same(t, current, conf.GetSettings(),
		"a user-owned path must not be rewritten, so no new snapshot is published")
	after, err := os.ReadFile(conf.ConfigPath)
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"a user-owned path must not be persisted; config.yaml must be unchanged")
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

	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  configuredModel,
		labels: filepath.Join(modelsDir, entry.ID, "gone_labels.txt"),
	}, false)

	assert.Equal(t, configuredModel, res.resolved.model,
		"the variant the configuration names must be kept when its own files are recoverable")
	assert.Equal(t, expectedLabels, res.resolved.labels,
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
		_ = o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)

		// Sibling-recovery path: configured model present, companion missing.
		_ = o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  installedModel,
			labels: filepath.Join(modelsDir, entry.ID, "gone_labels.txt"),
		}, false)

		// Queueing a correction is also done under the lock.
		res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
			model:  "/nonexistent/perch_v2.onnx",
			labels: "/nonexistent/perch_v2_labels.txt",
		}, false)
		o.queuePathCorrection(RegistryIDPerchV2, res)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("resolveFamilyPaths deadlocked while o.mu was held; it must not acquire o.mu")
	}
}
