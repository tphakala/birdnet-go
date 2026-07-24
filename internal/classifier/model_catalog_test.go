package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestEmbeddedCatalog_HasFilesWithModelRole(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		require.NotEmpty(t, entry.Files, "catalog entry %q has no files", entry.ID)

		// Shared-only entries (e.g. geomodels) use geomodel-role files instead of RoleModel.
		if IsSharedOnly(&entry) {
			continue
		}

		hasModel := false
		for _, f := range entry.Files {
			if f.Role == RoleModel {
				hasModel = true
				break
			}
		}
		assert.True(t, hasModel, "catalog entry %q has no file with role \"model\"", entry.ID)
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

	// Should have wildlife, bat, and geomodel categories (bird entries are currently hidden)
	require.Contains(t, grouped, CategoryWildlife)
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

	// Verify expected counts
	assert.Len(t, grouped[CategoryWildlife], 1, "expected 1 visible wildlife catalog entry")
	assert.Empty(t, grouped[CategoryBird], "expected 0 visible bird catalog entries")
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

	// 2 wildlife + 3 bird + 1 geomodel + 11 bat = 17 total
	// (bird: bsg-finland + the two hidden BirdNET v2.4 DFT-truncated variants)
	assert.Len(t, EmbeddedCatalog, 17, "expected 17 total catalog entries")
}

func TestVisibleCatalog_ExcludesHiddenEntries(t *testing.T) {
	t.Parallel()

	visible := VisibleCatalog()

	for _, entry := range visible {
		assert.False(t, entry.Hidden, "visible catalog should not contain hidden entry %q", entry.ID)
	}

	// Hidden entries should still be findable via GetCatalogEntry
	birdnetV3, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)
	assert.True(t, birdnetV3.Hidden)

	bsg, ok := GetCatalogEntry("bsg-finland")
	require.True(t, ok)
	assert.True(t, bsg.Hidden)

	// The DFT-truncated BirdNET v2.4 variants are hidden foundation entries.
	dftVariants := []string{"birdnet-v2.4-fp32-dfttrunc", "birdnet-v2.4-int8-arm-dfttrunc"}
	for _, id := range dftVariants {
		entry, ok := GetCatalogEntry(id)
		require.True(t, ok, "expected to find hidden catalog entry %q", id)
		assert.True(t, entry.Hidden, "entry %q must be hidden", id)
	}

	// The hidden variants must be directly absent from the visible set, not just
	// inferred from the count assertion below.
	for i := range visible {
		assert.NotContains(t, dftVariants, visible[i].ID,
			"hidden entry %q must be excluded from the visible catalog", visible[i].ID)
	}

	// Visible count should be total minus the 4 hidden entries
	// (birdnet-v3.0, bsg-finland, and the two BirdNET v2.4 DFT-truncated variants).
	// The hardcoded count is an intentional tripwire: a new hidden entry must
	// update it, forcing a conscious check that the exclusion is intended.
	assert.Len(t, visible, len(EmbeddedCatalog)-4)
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
}

// TestEmbeddedCatalog_DFTTruncatedVariants pins the identity of the two hidden
// BirdNET v2.4 DFT-truncated variant entries: their RemotePath, LocalName, size,
// and checksum are the authoritative values published on HuggingFace, and the
// entries are intentionally hidden ONNX drop-ins for the primary classifier. The
// literals mean an accidental edit, revert, or checksum drift is caught here.
func TestEmbeddedCatalog_DFTTruncatedVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id         string
		remotePath string
		sha256     string
		sizeBytes  int64
	}{
		{
			id:         "birdnet-v2.4-fp32-dfttrunc",
			remotePath: "BirdNET_v2.4_fp32_dfttrunc.onnx",
			sha256:     "3b72e88b3ad0c310a41adabccf8cf75b1a05daeeb40884ebd38038c91d0e423d",
			sizeBytes:  54068648,
		},
		{
			id:         "birdnet-v2.4-int8-arm-dfttrunc",
			remotePath: "BirdNET_v2.4_int8_arm_dfttrunc.onnx",
			sha256:     "7550498ba996064feca12005ff4133eb1d35741c4061376e7a987d8227518893",
			sizeBytes:  38727042,
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()

			entry, ok := GetCatalogEntry(tc.id)
			require.True(t, ok, "expected to find catalog entry %q", tc.id)
			assert.Equal(t, CategoryBird, entry.Category, "%q must be a bird model", tc.id)
			assert.True(t, entry.Hidden, "%q must be hidden (no primary-variant selector yet)", tc.id)
			assert.True(t, entry.RequiresONNX, "%q must require ONNX", tc.id)
			assert.Equal(t, permanentRegistryID, entry.RegistryID,
				"%q must map to the permanent BirdNET v2.4 registry ID", tc.id)
			assert.Equal(t, "tphakala/BirdNET-v2.4", entry.HuggingFaceRepo, "%q repo", tc.id)

			require.Len(t, entry.Files, 1, "%q must have exactly one (model) file", tc.id)
			f := entry.Files[0]
			assert.Equal(t, RoleModel, f.Role, "%q file must have the model role", tc.id)
			assert.Equal(t, tc.remotePath, f.RemotePath, "%q RemotePath", tc.id)
			assert.Equal(t, tc.remotePath, f.LocalName, "%q LocalName", tc.id)
			assert.Equal(t, tc.sha256, f.SHA256, "%q checksum", tc.id)
			assert.Equal(t, tc.sizeBytes, f.SizeBytes, "%q size", tc.id)
		})
	}
}
