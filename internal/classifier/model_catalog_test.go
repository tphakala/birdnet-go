package classifier

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVariantFilesByID(t *testing.T) {
	t.Parallel()

	modelFile := CatalogFile{RemotePath: "m.onnx", LocalName: "m.onnx", Role: RoleModel}
	int8File := CatalogFile{RemotePath: "m_int8.onnx", LocalName: "m_int8.onnx", Role: RoleModel}
	flatEntry := CatalogEntry{ID: "flat", Files: []CatalogFile{modelFile}}
	variantEntry := CatalogEntry{ID: "v", Variants: []CatalogVariant{
		{ID: "fp32", Default: true, Files: []CatalogFile{modelFile}},
		{ID: "int8-arm", Files: []CatalogFile{int8File}},
	}}

	t.Run("empty variant id returns entry Files", func(t *testing.T) {
		t.Parallel()
		files, ok := variantFilesByID(&flatEntry, "")
		require.True(t, ok)
		assert.Equal(t, flatEntry.Files, files)
	})

	t.Run("empty variant id on a variant entry returns top-level Files", func(t *testing.T) {
		t.Parallel()
		// On a resolved entry, top-level Files is the default variant's files.
		files, ok := variantFilesByID(&variantEntry, "")
		require.True(t, ok)
		assert.Equal(t, variantEntry.Files, files)
	})

	t.Run("selects the named non-default variant", func(t *testing.T) {
		t.Parallel()
		files, ok := variantFilesByID(&variantEntry, "int8-arm")
		require.True(t, ok)
		require.Len(t, files, 1)
		assert.Equal(t, "m_int8.onnx", files[0].LocalName)
	})

	t.Run("unknown variant id reports not found", func(t *testing.T) {
		t.Parallel()
		files, ok := variantFilesByID(&variantEntry, "does-not-exist")
		assert.False(t, ok)
		assert.Nil(t, files)
	})

	t.Run("non-empty variant id on a flat entry reports not found", func(t *testing.T) {
		t.Parallel()
		// A flat entry has no variants, so any non-empty selection is unknown and
		// must be rejected rather than silently resolving to the flat Files.
		files, ok := variantFilesByID(&flatEntry, "int8-arm")
		assert.False(t, ok)
		assert.Nil(t, files)
	})
}

func TestEmbeddedCatalog_UniqueIDs(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(EmbeddedCatalog))
	for _, entry := range EmbeddedCatalog {
		require.False(t, seen[entry.ID], "duplicate catalog ID: %s", entry.ID)
		seen[entry.ID] = true
	}
}

func TestEmbeddedCatalog_ValidRegistryIDs(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		// Shared-only entries (e.g. geomodels) have no RegistryID.
		if IsSharedOnly(&entry) {
			continue
		}
		assert.NotEmpty(t, entry.RegistryID, "catalog entry %q must have a RegistryID", entry.ID)
		_, exists := ModelRegistry[entry.RegistryID]
		assert.True(t, exists, "catalog entry %q references unknown RegistryID %q", entry.ID, entry.RegistryID)
	}
}

// assertFilesHaveChecksums asserts every file carries a valid 32-byte hex SHA-256
// and a positive size. It validates that the checksum DECODES to a 32-byte digest,
// not just that it is 64 characters: a 64-char non-hex string would never match a
// real digest and so would fail closed at download time, but it has no business in
// the catalog. hex.DecodeString("") returns no error, so the length check is what
// rejects an empty checksum. label names the entry (and variant) for failures.
func assertFilesHaveChecksums(t *testing.T, label string, files []CatalogFile) {
	t.Helper()
	for _, f := range files {
		raw, err := hex.DecodeString(f.SHA256)
		if assert.NoErrorf(t, err,
			"catalog %q file %q must carry a hex SHA-256", label, f.RemotePath) {
			assert.Lenf(t, raw, sha256.Size,
				"catalog %q file %q SHA-256 must decode to 32 bytes", label, f.RemotePath)
		}
		assert.Positivef(t, f.SizeBytes,
			"catalog %q file %q must carry a positive SizeBytes", label, f.RemotePath)
	}
}

// TestEmbeddedCatalog_AllFilesHaveChecksums verifies every downloadable file in the
// catalog carries a real SHA-256 and a non-zero size. An empty SHA-256 makes
// verifySHA256 a no-op, so the file would be downloaded and loaded with no integrity
// check at all; a zero size makes the download progress bar meaningless. The invariant
// must hold for every entry, including Hidden ones, because Hidden is a UI and install
// gate, not an integrity guarantee: a Hidden entry can still be reached by config or a
// future un-hide, and every file the gallery can fetch must be verifiable. Variant
// entries carry files under Variants (their top-level Files is empty and resolved at
// load time), so both are checked.
func TestEmbeddedCatalog_AllFilesHaveChecksums(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		assertFilesHaveChecksums(t, entry.ID, entry.Files)
		for _, v := range entry.Variants {
			assertFilesHaveChecksums(t, entry.ID+"/"+v.ID, v.Files)
		}
	}
}

func TestResolveVariantDefaults(t *testing.T) {
	// setActiveCatalog mutates a package global, so this test runs serially.
	fp32 := CatalogFile{RemotePath: "m_fp32.onnx", LocalName: "m_fp32.onnx", Role: RoleModel, SHA256: "a", SizeBytes: 10}
	int8File := CatalogFile{RemotePath: "m_int8.onnx", LocalName: "m_int8.onnx", Role: RoleModel, SHA256: "b", SizeBytes: 5}

	t.Run("resolves the default variant into Files and preserves Variants", func(t *testing.T) {
		resetActiveCatalog(t)
		setActiveCatalog([]CatalogEntry{{
			ID: "multi", Name: "Multi", Category: CategoryBird, Version: "1",
			Variants: []CatalogVariant{
				{ID: "int8-arm", Files: []CatalogFile{int8File}},
				{ID: "fp32", Default: true, Files: []CatalogFile{fp32}},
			},
		}})
		got, ok := GetCatalogEntry("multi")
		require.True(t, ok)
		require.Len(t, got.Files, 1)
		assert.Equal(t, "m_fp32.onnx", got.Files[0].LocalName, "Files must resolve to the default variant")
		assert.Len(t, got.Variants, 2, "Variants must be preserved for the gallery and install path")
	})

	t.Run("falls back to the first variant when none is marked default", func(t *testing.T) {
		resetActiveCatalog(t)
		setActiveCatalog([]CatalogEntry{{
			ID: "nodefault", Name: "NoDefault", Category: CategoryBird, Version: "1",
			Variants: []CatalogVariant{
				{ID: "int8-arm", Files: []CatalogFile{int8File}},
				{ID: "fp32", Files: []CatalogFile{fp32}},
			},
		}})
		got, ok := GetCatalogEntry("nodefault")
		require.True(t, ok)
		require.Len(t, got.Files, 1)
		assert.Equal(t, "m_int8.onnx", got.Files[0].LocalName, "Files must resolve to the first variant")
	})

	t.Run("picks the first Default-flagged variant when several set Default", func(t *testing.T) {
		resetActiveCatalog(t)
		fp16 := CatalogFile{RemotePath: "m_fp16.onnx", LocalName: "m_fp16.onnx", Role: RoleModel, SHA256: "c", SizeBytes: 7}
		setActiveCatalog([]CatalogEntry{{
			ID: "multidefault", Name: "MultiDefault", Category: CategoryBird, Version: "1",
			Variants: []CatalogVariant{
				{ID: "int8-arm", Files: []CatalogFile{int8File}},
				{ID: "fp32", Default: true, Files: []CatalogFile{fp32}},
				{ID: "fp16", Default: true, Files: []CatalogFile{fp16}},
			},
		}})
		got, ok := GetCatalogEntry("multidefault")
		require.True(t, ok)
		require.Len(t, got.Files, 1)
		assert.Equal(t, "m_fp32.onnx", got.Files[0].LocalName, "the first Default-flagged variant wins deterministically")
	})

	t.Run("leaves a single-variant entry untouched", func(t *testing.T) {
		resetActiveCatalog(t)
		setActiveCatalog([]CatalogEntry{customEntry("plain")})
		got, ok := GetCatalogEntry("plain")
		require.True(t, ok)
		require.Len(t, got.Files, 1)
		assert.Equal(t, "plain.onnx", got.Files[0].LocalName)
		assert.Empty(t, got.Variants)
	})

	t.Run("does not mutate the caller's entries", func(t *testing.T) {
		resetActiveCatalog(t)
		entries := []CatalogEntry{{
			ID: "immut", Name: "Immut", Category: CategoryBird, Version: "1",
			Variants: []CatalogVariant{{ID: "fp32", Default: true, Files: []CatalogFile{fp32}}},
		}}
		setActiveCatalog(entries)
		assert.Empty(t, entries[0].Files, "resolution must not mutate the caller's entries (may be EmbeddedCatalog)")
	})
}

// hasModelRoleFile reports whether files contains a file with the model role.
func hasModelRoleFile(files []CatalogFile) bool {
	for i := range files {
		if files[i].Role == RoleModel {
			return true
		}
	}
	return false
}

func TestEmbeddedCatalog_HasFilesWithModelRole(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		// Variant entries carry files under Variants; their top-level Files is empty
		// in the raw embedded catalog and resolved to the default variant at load
		// time. Every variant must ship files including a model-role file.
		if len(entry.Variants) > 0 {
			for _, v := range entry.Variants {
				// BuiltIn baseline variants (the embedded primary model) carry no
				// files: there is nothing to download and ScanInstalled reports them
				// installed unconditionally. Every other variant must ship a
				// model-role file.
				if v.BuiltIn {
					assert.Emptyf(t, v.Files, "catalog entry %q built-in variant %q must declare no files", entry.ID, v.ID)
					continue
				}
				require.NotEmptyf(t, v.Files, "catalog entry %q variant %q has no files", entry.ID, v.ID)
				assert.Truef(t, hasModelRoleFile(v.Files),
					"catalog entry %q variant %q has no file with role \"model\"", entry.ID, v.ID)
			}
			continue
		}

		require.NotEmpty(t, entry.Files, "catalog entry %q has no files", entry.ID)

		// Shared-only entries (e.g. geomodels) use geomodel-role files instead of RoleModel.
		if IsSharedOnly(&entry) {
			continue
		}

		assert.True(t, hasModelRoleFile(entry.Files), "catalog entry %q has no file with role \"model\"", entry.ID)
	}
}

func TestEmbeddedCatalog_ValidCategories(t *testing.T) {
	t.Parallel()

	validCategories := map[string]bool{CategoryWildlife: true, CategoryBird: true, CategoryBat: true, CategoryGeomodel: true}
	for _, entry := range EmbeddedCatalog {
		assert.True(t, validCategories[entry.Category],
			"catalog entry %q has invalid category %q (must be \"wildlife\", \"bird\", \"bat\", or \"geomodel\")", entry.ID, entry.Category)
	}
}

func TestGetCatalogEntry_Found(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok, "expected to find catalog entry battybirdnet-eu")
	assert.Equal(t, "battybirdnet-eu", entry.ID)
	assert.Equal(t, "BattyBirdNET EU", entry.Name)
	assert.Equal(t, CategoryBat, entry.Category)
	assert.Equal(t, "Bat", entry.RegistryID)
}

func TestGetCatalogEntry_NotFound(t *testing.T) {
	t.Parallel()

	_, ok := GetCatalogEntry("nonexistent")
	assert.False(t, ok, "expected nonexistent entry to return false")
}

func TestCatalogByCategory(t *testing.T) {
	t.Parallel()

	grouped := CatalogByCategory()

	// Should have wildlife, bird, bat, and geomodel categories. The only visible bird
	// entry is birdnet-v2.4 (bsg-finland stays hidden).
	require.Contains(t, grouped, CategoryWildlife)
	require.Contains(t, grouped, CategoryBird)
	require.Contains(t, grouped, CategoryBat)
	require.Contains(t, grouped, CategoryGeomodel)

	// All entries in each group should have the matching category
	for _, entry := range grouped[CategoryWildlife] {
		assert.Equal(t, CategoryWildlife, entry.Category)
	}
	for _, entry := range grouped[CategoryBat] {
		assert.Equal(t, CategoryBat, entry.Category)
	}
	for _, entry := range grouped[CategoryGeomodel] {
		assert.Equal(t, CategoryGeomodel, entry.Category)
	}

	for _, entry := range grouped[CategoryBird] {
		assert.Equal(t, CategoryBird, entry.Category)
	}

	// Verify expected counts
	assert.Len(t, grouped[CategoryWildlife], 2, "expected 2 visible wildlife catalog entries (perch-v2, birdnet-v3.0)")
	assert.Len(t, grouped[CategoryBird], 1, "expected 1 visible bird catalog entry (birdnet-v2.4)")
	assert.Len(t, grouped[CategoryGeomodel], 1, "expected 1 visible geomodel catalog entry")
	assert.Len(t, grouped[CategoryBat], 11, "expected 11 bat catalog entries")
}

func TestEmbeddedCatalog_BatEntriesHaveEmbeddingsFile(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		if entry.Category != CategoryBat {
			continue
		}

		embeddingsCount := 0
		for _, f := range entry.Files {
			if f.Role != RoleEmbeddings {
				continue
			}
			embeddingsCount++
			// LocalName is kept stable for drop-in compatibility with existing
			// installs; RemotePath points at the DFT-truncated backbone (bit-exact,
			// ~2x faster). The two intentionally differ, so assert both. Size and
			// SHA256 are pinned to literals (not the embeddingsSizeBytes/embeddingsSHA256
			// constants) so this locks the exact expected file content: comparing the
			// field to the constant it is assigned from would be a tautology, whereas a
			// literal catches an accidental constant change and forces a deliberate model
			// swap to update the test too. No break: validate every embeddings file so a
			// future entry carrying a second, mismatched one would still fail.
			assert.Equal(t, "birdnet-v24-embeddings.onnx", f.LocalName,
				"bat entry %q should use shared embeddings file", entry.ID)
			assert.Equal(t, "birdnet-v2.4-embeddings-fp32-dfttrunc.onnx", f.RemotePath,
				"bat entry %q should fetch the DFT-truncated backbone", entry.ID)
			assert.Equal(t, int64(58763257), f.SizeBytes,
				"bat entry %q embeddings size should match the DFT-truncated backbone", entry.ID)
			assert.Equal(t, "b91139d3c63d55d742779a56531078bc88366a09bcc9bd6a9b703d425914c380", f.SHA256,
				"bat entry %q embeddings checksum should match the DFT-truncated backbone", entry.ID)
		}
		assert.Equal(t, 1, embeddingsCount, "bat entry %q must have exactly one embeddings file", entry.ID)
	}
}

func TestEmbeddedCatalog_EntryCount(t *testing.T) {
	t.Parallel()

	// 2 wildlife + 2 bird + 1 geomodel + 11 bat = 16 total
	// (bird: bsg-finland + the collapsed hidden BirdNET v2.4 foundation entry)
	assert.Len(t, EmbeddedCatalog, 16, "expected 16 total catalog entries")
}

func TestVisibleCatalog_ExcludesHiddenEntries(t *testing.T) {
	t.Parallel()

	visible := VisibleCatalog()

	for _, entry := range visible {
		assert.False(t, entry.Hidden, "visible catalog should not contain hidden entry %q", entry.ID)
	}

	// birdnet-v3.0 is now visible (un-hidden): the public HF repo, pinned
	// checksums, and per-variant RAM floors make it safe to offer in the gallery.
	birdnetV3, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)
	assert.False(t, birdnetV3.Hidden, "birdnet-v3.0 must be visible")
	foundV3 := false
	for i := range visible {
		if visible[i].ID == "birdnet-v3.0" {
			foundV3 = true
			break
		}
	}
	assert.True(t, foundV3, "birdnet-v3.0 must appear in the visible catalog")

	// Hidden entries should still be findable via GetCatalogEntry.
	bsg, ok := GetCatalogEntry("bsg-finland")
	require.True(t, ok)
	assert.True(t, bsg.Hidden)

	// The BirdNET v2.4 entry is now visible (un-hidden): it is wired into its own
	// variant set (embedded BuiltIn baseline + DFT-truncated builds) so the gallery
	// can offer an in-place optimize swap. bsg-finland remains the only hidden
	// foundation entry.
	birdnetV24, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)
	assert.False(t, birdnetV24.Hidden, "birdnet-v2.4 must be visible")

	hiddenFoundation := []string{"bsg-finland"}
	for _, id := range hiddenFoundation {
		entry, ok := GetCatalogEntry(id)
		require.True(t, ok, "expected to find hidden catalog entry %q", id)
		assert.True(t, entry.Hidden, "entry %q must be hidden", id)
	}

	// The hidden entries must be directly absent from the visible set, not just
	// inferred from the count assertion below.
	for i := range visible {
		assert.NotContains(t, hiddenFoundation, visible[i].ID,
			"hidden entry %q must be excluded from the visible catalog", visible[i].ID)
	}

	// Visible count should be total minus the 1 hidden entry (bsg-finland; the
	// BirdNET v2.4 entry is now visible). The hardcoded count is an intentional
	// tripwire: a new hidden entry must update it, forcing a conscious check that the
	// exclusion is intended.
	assert.Len(t, visible, len(EmbeddedCatalog)-1)
}

func TestGetCatalogEntry_BSGFinland(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("bsg-finland")
	require.True(t, ok, "expected to find catalog entry bsg-finland")
	assert.Equal(t, "bsg-finland", entry.ID)
	assert.Equal(t, "BSG Finland v4.4", entry.Name)
	assert.Equal(t, CategoryBird, entry.Category)
	assert.Equal(t, RegistryIDBSG, entry.RegistryID)
	assert.Equal(t, "Finland", entry.Region)
	hasModel := false
	hasLabels := false
	for _, f := range entry.Files {
		switch f.Role {
		case RoleModel:
			hasModel = true
		case RoleLabels:
			hasLabels = true
		}
	}
	assert.True(t, hasModel, "BSG entry must have a model file")
	assert.True(t, hasLabels, "BSG entry must have a labels file")
}

func TestGetCatalogEntry_BirdNETv30(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok, "expected to find catalog entry birdnet-v3.0")
	assert.Equal(t, "birdnet-v3.0", entry.ID)
	assert.Equal(t, "BirdNET v3.0", entry.Name)
	assert.Equal(t, RegistryIDBirdNETV3, entry.RegistryID)
	assert.Equal(t, CategoryWildlife, entry.Category)
	assert.False(t, entry.Hidden, "birdnet-v3.0 is visible in the gallery")
	assert.True(t, entry.CommercialUse, "birdnet-v3.0 is CC-BY-SA-4.0, which permits commercial use")
}

// TestEmbeddedCatalog_VariantRequirementsPopulated pins the per-variant MinRAMMB
// floors, the fp16 exclude token, and the BirdNET v3.0 benchmark data. These
// values are mirrored from the acoustic-models manifests and drive the hardware
// recommender: without the RAM floors the recommender cannot keep heavy variants
// off low-RAM hosts, and without at least two comparable benchmarks per entry the
// latency term stays inert. Literals catch an accidental edit or a manifest drift.
func TestEmbeddedCatalog_VariantRequirementsPopulated(t *testing.T) {
	t.Parallel()

	// Expected MinRAMMB per (entry, variant), mirrored from the manifests.
	wantRAM := map[string]map[string]int{
		"birdnet-v3.0": {"fp32": 800, "fp16": 1100},
		"perch-v2":     {"fp32": 700, "no-dft-fp32": 750, "int8-arm": 350},
		"birdnet-v2.4": {"fp32-dfttrunc": 250, "int8-arm-dfttrunc": 250},
	}
	for entryID, variants := range wantRAM {
		entry, ok := GetCatalogEntry(entryID)
		require.Truef(t, ok, "expected catalog entry %q", entryID)
		for variantID, wantMB := range variants {
			v := variantByID(t, &entry, variantID)
			assert.Equalf(t, wantMB, v.Requirements.MinRAMMB, "%s/%s MinRAMMB", entryID, variantID)
		}
	}

	// The fp16 variant excludes the known-bad Intel gen12 iGPU path.
	v30, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)
	fp16 := variantByID(t, &v30, "fp16")
	assert.Contains(t, fp16.Requirements.Excludes, "openvino-gpu-intel-gen12", "fp16 must exclude the gen12 iGPU path")

	// BirdNET v3.0 is the only family with measured benchmarks. Pin every row
	// exactly (device, backend, latency, RSS): the latencies drive the
	// recommender's ranking, so a wrong value is a silent behavior change, not a
	// count mismatch. Both global variants carry an rpi5-a76 onnxruntime-cpu
	// measurement, the two comparable members the recommender needs to rank on
	// latency.
	fp32 := variantByID(t, &v30, "fp32")
	assert.Len(t, fp32.Benchmarks, 5, "fp32 benchmark rows")
	assert.Len(t, fp16.Benchmarks, 3, "fp16 benchmark rows")
	benchWant := []struct {
		variantID, device, backend string
		latencyMs, rssMB           int
	}{
		{"fp32", "rpi5-a76", "openvino-cpu", 168, 0},
		{"fp32", "rpi5-a76", "onnxruntime-cpu", 363, 685},
		{"fp32", "rpi4b-a72", "onnxruntime-cpu", 874, 688},
		{"fp32", "x86-i7-1260P", "onnxruntime-cpu", 70, 0},
		{"fp32", "x86-i7-1260P", "openvino-gpu", 95, 0},
		{"fp16", "rpi5-a76", "onnxruntime-cpu", 381, 480},
		{"fp16", "rpi4b-a72", "onnxruntime-cpu", 887, 929},
		{"fp16", "x86-i7-1260P", "openvino-gpu", 81, 0},
	}
	for _, w := range benchWant {
		t.Run(w.variantID+"/"+w.device+"/"+w.backend, func(t *testing.T) {
			t.Parallel()
			b, ok := findBenchmark(variantByID(t, &v30, w.variantID), w.device, w.backend)
			require.Truef(t, ok, "%s missing benchmark %s/%s", w.variantID, w.device, w.backend)
			assert.Equalf(t, w.latencyMs, b.LatencyMs, "%s %s/%s latency", w.variantID, w.device, w.backend)
			assert.Equalf(t, w.rssMB, b.RSSMB, "%s %s/%s rss", w.variantID, w.device, w.backend)
		})
	}
}

// findBenchmark returns the variant's benchmark measured on the given device
// with the given backend, and whether one exists.
func findBenchmark(v *CatalogVariant, device, backend string) (Benchmark, bool) {
	for i := range v.Benchmarks {
		if v.Benchmarks[i].Device == device && v.Benchmarks[i].Backend == backend {
			return v.Benchmarks[i], true
		}
	}
	return Benchmark{}, false
}

// TestEmbeddedCatalog_BirdNETv24Variants pins the identity of the collapsed hidden
// BirdNET v2.4 entry and its two DFT-truncated hardware variants: their RemotePath,
// LocalName, size, and checksum are the authoritative values published on HuggingFace.
// The entry is intentionally hidden (no primary-variant selector yet) and keyed to the
// permanent BirdNET v2.4 registry ID. Literals mean an accidental edit, revert, or
// checksum drift is caught here.
func TestEmbeddedCatalog_BirdNETv24Variants(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok, "expected to find catalog entry birdnet-v2.4")
	assert.Equal(t, CategoryBird, entry.Category, "birdnet-v2.4 must be a bird model")
	assert.False(t, entry.Hidden, "birdnet-v2.4 must be visible (wired into its variant set)")
	assert.False(t, entry.RequiresONNX, "birdnet-v2.4 ORT need is per-variant, not entry-level")
	assert.Equal(t, permanentRegistryID, entry.RegistryID,
		"birdnet-v2.4 must map to the permanent BirdNET v2.4 registry ID")
	assert.Equal(t, "tphakala/BirdNET-v2.4", entry.HuggingFaceRepo, "birdnet-v2.4 repo")
	assert.Equal(t, birdnetV24SpeciesCount, entry.SpeciesCount,
		"birdnet-v2.4 must report the embedded label count")

	cases := []struct {
		variantID    string
		builtIn      bool
		remotePath   string
		sha256       string
		sizeBytes    int64
		isDefault    bool
		speciesCount int
	}{
		{
			variantID:    "builtin",
			builtIn:      true,
			isDefault:    true,
			speciesCount: birdnetV24SpeciesCount,
		},
		{
			variantID:    "fp32-dfttrunc",
			remotePath:   "BirdNET_v2.4_fp32_dfttrunc.onnx",
			sha256:       "3b72e88b3ad0c310a41adabccf8cf75b1a05daeeb40884ebd38038c91d0e423d",
			sizeBytes:    54068648,
			isDefault:    false,
			speciesCount: birdnetV24SpeciesCount,
		},
		{
			variantID:    "int8-arm-dfttrunc",
			remotePath:   "BirdNET_v2.4_int8_arm_dfttrunc.onnx",
			sha256:       "7550498ba996064feca12005ff4133eb1d35741c4061376e7a987d8227518893",
			sizeBytes:    38727042,
			isDefault:    false,
			speciesCount: birdnetV24SpeciesCount,
		},
	}
	require.Lenf(t, entry.Variants, len(cases), "birdnet-v2.4 must have exactly %d variants", len(cases))

	defaults := 0
	for i, tc := range cases {
		v := entry.Variants[i]
		assert.Equalf(t, tc.variantID, v.ID, "variant %d id", i)
		assert.Equalf(t, tc.builtIn, v.BuiltIn, "variant %q BuiltIn flag", tc.variantID)
		assert.Equalf(t, tc.isDefault, v.Default, "variant %q default flag", tc.variantID)
		assert.Falsef(t, v.Legacy, "variant %q must not be Legacy", tc.variantID)
		assert.Equalf(t, tc.speciesCount, v.SpeciesCount, "variant %q species count", tc.variantID)
		if v.Default {
			defaults++
		}
		if tc.builtIn {
			assert.Emptyf(t, v.Files, "variant %q (built-in) must carry no files", tc.variantID)
			// The built-in baseline must NOT recommend an ONNX backend: a 0-byte size
			// tie-break would otherwise let it beat a real DFT build and suppress the
			// optimize offer. It advertises tflite as the recommended path.
			assert.Truef(t, v.Backends["tflite"].Recommended, "built-in variant must recommend tflite")
			assert.Falsef(t, v.Backends["onnxruntime-cpu"].Recommended, "built-in variant must not recommend ORT")
			continue
		}
		require.Lenf(t, v.Files, 1, "variant %q must have exactly one (model) file", tc.variantID)
		f := v.Files[0]
		assert.Equalf(t, RoleModel, f.Role, "variant %q file must have the model role", tc.variantID)
		assert.Equalf(t, tc.remotePath, f.RemotePath, "variant %q RemotePath", tc.variantID)
		assert.Equalf(t, tc.remotePath, f.LocalName, "variant %q LocalName", tc.variantID)
		assert.Equalf(t, tc.sha256, f.SHA256, "variant %q checksum", tc.variantID)
		assert.Equalf(t, tc.sizeBytes, f.SizeBytes, "variant %q size", tc.variantID)
	}
	assert.Equal(t, 1, defaults, "birdnet-v2.4 must have exactly one Default variant")
}

// TestVariantNeedsONNX covers the per-variant ORT gate: the BuiltIn baseline runs
// on the embedded TFLite model (no ORT), the DFT-truncated builds are ONNX-only, and
// an empty id resolves to the default (baseline) variant.
func TestVariantNeedsONNX(t *testing.T) {
	t.Parallel()

	v24, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)

	assert.False(t, VariantNeedsONNX(&v24, "builtin"), "the embedded baseline runs on TFLite, no ORT")
	assert.False(t, VariantNeedsONNX(&v24, ""), "empty id resolves to the default (baseline), no ORT")
	assert.True(t, VariantNeedsONNX(&v24, "fp32-dfttrunc"), "the FP32 DFT build is ONNX-only")
	assert.True(t, VariantNeedsONNX(&v24, "int8-arm-dfttrunc"), "the INT8 DFT build is ONNX-only")

	// A known ONNX variant resolves through the variant branch.
	perch, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	assert.True(t, VariantNeedsONNX(&perch, "fp32"), "perch fp32 is an ONNX variant")

	// An unknown variant id falls back to the entry-level RequiresONNX flag.
	assert.True(t, VariantNeedsONNX(&perch, "no-such-variant"),
		"an unknown variant id on an ONNX entry falls back to the entry flag")
	assert.False(t, VariantNeedsONNX(&v24, "no-such-variant"),
		"an unknown variant id on v2.4 falls back to its false entry flag")

	// A flat entry (no variants) uses the entry-level flag directly.
	bsg, ok := GetCatalogEntry("bsg-finland")
	require.True(t, ok)
	require.Empty(t, bsg.Variants, "bsg-finland is a flat entry")
	assert.True(t, VariantNeedsONNX(&bsg, ""), "a flat ONNX entry needs ORT")

	// A TFLite backend bypasses ORT only when it is actually Supported. A remote
	// manifest may list "tflite" with Supported:false for an unavailable backend
	// (the Perch manifests do), and key presence alone must not skip the requirement.
	synthetic := CatalogEntry{
		ID: "synthetic-tflite",
		Variants: []CatalogVariant{
			{ID: "tflite-ok", Backends: map[string]BackendSupport{"tflite": {Supported: true}}},
			{ID: "tflite-unsupported", Backends: map[string]BackendSupport{"tflite": {Supported: false}}},
			{ID: "onnx-only", Backends: map[string]BackendSupport{"onnxruntime-cpu": {Supported: true}}},
		},
	}
	assert.False(t, VariantNeedsONNX(&synthetic, "tflite-ok"), "a supported TFLite backend avoids ORT")
	assert.True(t, VariantNeedsONNX(&synthetic, "tflite-unsupported"), "an unsupported TFLite entry must not bypass ORT")
	assert.True(t, VariantNeedsONNX(&synthetic, "onnx-only"), "a variant with only ONNX backends needs ORT")
}

// TestIsPermanentEntry verifies the permanent-entry predicate.
func TestIsPermanentEntry(t *testing.T) {
	t.Parallel()

	v24, ok := GetCatalogEntry("birdnet-v2.4")
	require.True(t, ok)
	assert.True(t, IsPermanentEntry(&v24), "birdnet-v2.4 is the permanent entry")

	perch, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	assert.False(t, IsPermanentEntry(&perch), "perch-v2 is not permanent")
	assert.False(t, IsPermanentEntry(nil), "nil is not permanent")
}

// TestEmbeddedCatalog_GlobalVariantResolution verifies the multi-variant global
// entries (Perch v2, BirdNET v3.0) resolve their default variant into entry.Files
// with the model and labels LocalName + checksum unchanged from the pre-variant flat
// entries (so existing installs are undisturbed), carry the shared geomodel + taxonomy
// companions, and declare exactly one Default variant with no Legacy variant.
func TestEmbeddedCatalog_GlobalVariantResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id          string
		variantIDs  []string
		modelLocal  string
		modelSHA    string
		labelsLocal string
		labelsSHA   string
	}{
		{
			id:          "perch-v2",
			variantIDs:  []string{"fp32", "no-dft-fp32", "int8-arm"},
			modelLocal:  "perch_v2.onnx",
			modelSHA:    "bf0c8467a924cb074663970ca4a0ab1e143602121930209657d0dff5d5cefa1f",
			labelsLocal: "perch_v2_labels.txt",
			labelsSHA:   "e4d5c0397d8fb08bf90c6b13a34810af53504faad927e472fcc567793c9de057",
		},
		{
			id:          "birdnet-v3.0",
			variantIDs:  []string{"fp32", "fp16"},
			modelLocal:  "birdnet_v3.0_fp32.onnx",
			modelSHA:    "05535c3ef6ce3f9e523706dd3e144cb6db96bc202e9047f4973961256acbf997",
			labelsLocal: "birdnet_v3.0_labels.txt",
			labelsSHA:   "4f4ef82f1704c66cf4da9f59757c12baa34ff98863fa2627e33c302fc92997aa",
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			entry, ok := GetCatalogEntry(tc.id)
			require.Truef(t, ok, "expected to find catalog entry %q", tc.id)

			gotIDs := make([]string, len(entry.Variants))
			defaults := 0
			for i := range entry.Variants {
				gotIDs[i] = entry.Variants[i].ID
				if entry.Variants[i].Default {
					defaults++
				}
				assert.Falsef(t, entry.Variants[i].Legacy, "%s variant %q must not be Legacy", tc.id, entry.Variants[i].ID)
			}
			// Global variants come first (slices.Concat(globals, regional...)),
			// followed by the generated regional tiles.
			require.GreaterOrEqualf(t, len(gotIDs), len(tc.variantIDs), "%s exposes at least its global variants", tc.id)
			assert.Equalf(t, tc.variantIDs, gotIDs[:len(tc.variantIDs)], "%s global variant ids come first, in order", tc.id)
			assert.Lenf(t, gotIDs, len(tc.variantIDs)+regionalTilesPerFamily, "%s exposes its global variants plus %d regional tiles", tc.id, regionalTilesPerFamily)
			assert.Equalf(t, 1, defaults, "%s must have exactly one Default variant (regional tiles are never Default)", tc.id)

			// Resolved Files: default model + labels LocalName/sha unchanged, companions present.
			var model, labels *CatalogFile
			hasGeomodel, hasTaxonomy := false, false
			for i := range entry.Files {
				switch entry.Files[i].Role {
				case RoleModel:
					model = &entry.Files[i]
				case RoleLabels:
					labels = &entry.Files[i]
				case RoleGeomodelModel, RoleGeomodelLabels:
					hasGeomodel = true
				case RoleTaxonomy:
					hasTaxonomy = true
				}
			}
			require.NotNilf(t, model, "%s resolved Files must contain a model file", tc.id)
			require.NotNilf(t, labels, "%s resolved Files must contain a labels file", tc.id)
			assert.Equalf(t, tc.modelLocal, model.LocalName, "%s default model LocalName", tc.id)
			assert.Equalf(t, tc.modelSHA, model.SHA256, "%s default model checksum", tc.id)
			assert.Equalf(t, tc.labelsLocal, labels.LocalName, "%s labels LocalName", tc.id)
			assert.Equalf(t, tc.labelsSHA, labels.SHA256, "%s labels checksum", tc.id)
			assert.Truef(t, hasGeomodel, "%s resolved Files must carry the geomodel companion", tc.id)
			assert.Truef(t, hasTaxonomy, "%s resolved Files must carry the taxonomy companion", tc.id)
		})
	}
}

// fileRoles returns the set of file roles present in files.
func fileRoles(files []CatalogFile) map[string]bool {
	roles := make(map[string]bool, len(files))
	for i := range files {
		roles[files[i].Role] = true
	}
	return roles
}

// TestEmbeddedCatalog_VariantFilesSelfContained verifies that EVERY variant (not
// just the resolved default) carries the companions it needs. resolveVariantDefaults
// sets entry.Files = variant.Files without merging, so a variant that omits the
// geomodel/taxonomy companion would silently break the range-filter wiring once a
// variant selector installs it. Perch v2 and BirdNET v3.0 variants must each carry
// model + labels + geomodel + taxonomy; BirdNET v2.4 variants deliberately carry only
// the model (embedded labels, no geomodel).
func TestEmbeddedCatalog_VariantFilesSelfContained(t *testing.T) {
	t.Parallel()

	withCompanions := map[string]bool{"perch-v2": true, "birdnet-v3.0": true}
	modelOnly := map[string]bool{"birdnet-v2.4": true}

	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		wantCompanions := withCompanions[entry.ID]
		wantModelOnly := modelOnly[entry.ID]
		if !wantCompanions && !wantModelOnly {
			continue
		}
		require.NotEmptyf(t, entry.Variants, "entry %q expected to carry variants", entry.ID)
		for j := range entry.Variants {
			v := &entry.Variants[j]
			// The BuiltIn baseline (embedded primary model) carries no files by
			// design; it is not a downloadable, self-contained variant.
			if v.BuiltIn {
				assert.Emptyf(t, v.Files, "%s/%s built-in variant must carry no files", entry.ID, v.ID)
				continue
			}
			roles := fileRoles(v.Files)
			assert.Truef(t, roles[RoleModel], "%s/%s must carry a model file", entry.ID, v.ID)
			if wantCompanions {
				assert.Truef(t, roles[RoleLabels], "%s/%s must carry a labels file", entry.ID, v.ID)
				assert.Truef(t, roles[RoleGeomodelModel], "%s/%s must carry the geomodel model companion", entry.ID, v.ID)
				assert.Truef(t, roles[RoleGeomodelLabels], "%s/%s must carry the geomodel labels companion", entry.ID, v.ID)
				assert.Truef(t, roles[RoleTaxonomy], "%s/%s must carry the taxonomy companion", entry.ID, v.ID)
			}
			if wantModelOnly {
				assert.Falsef(t, roles[RoleGeomodelModel], "%s/%s must NOT carry a geomodel companion (uses embedded labels)", entry.ID, v.ID)
				assert.Falsef(t, roles[RoleTaxonomy], "%s/%s must NOT carry a taxonomy companion", entry.ID, v.ID)
				assert.Falsef(t, roles[RoleLabels], "%s/%s must NOT carry a downloaded labels file (embedded)", entry.ID, v.ID)
			}
		}
	}
}

// TestEmbeddedCatalog_VariantEntriesHaveNoTopLevelFiles pins the mutual-exclusion
// invariant on the RAW embedded catalog: an entry that declares Variants must leave
// its top-level Files empty (resolveVariantDefaults fills it from the default variant
// at load time, and validateCatalogEntryFiles rejects an entry that sets both). This
// reads EmbeddedCatalog directly, not GetCatalogEntry, because the latter returns the
// resolved catalog where Files is intentionally populated.
func TestEmbeddedCatalog_VariantEntriesHaveNoTopLevelFiles(t *testing.T) {
	t.Parallel()

	for i := range EmbeddedCatalog {
		entry := &EmbeddedCatalog[i]
		if len(entry.Variants) == 0 {
			continue
		}
		assert.Emptyf(t, entry.Files,
			"catalog entry %q declares variants, so its top-level Files must be empty (resolved at load time; the loader rejects an entry that sets both)", entry.ID)
	}
}
