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
		assert.NotEmpty(t, entry.RegistryID, "catalog entry %q must have a RegistryID", entry.ID)
		_, exists := ModelRegistry[entry.RegistryID]
		assert.True(t, exists, "catalog entry %q references unknown RegistryID %q", entry.ID, entry.RegistryID)
	}
}

func TestEmbeddedCatalog_HasFilesWithModelRole(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		require.NotEmpty(t, entry.Files, "catalog entry %q has no files", entry.ID)

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

	validCategories := map[string]bool{CategoryWildlife: true, CategoryBird: true, CategoryBat: true}
	for _, entry := range EmbeddedCatalog {
		assert.True(t, validCategories[entry.Category],
			"catalog entry %q has invalid category %q (must be \"wildlife\", \"bird\", or \"bat\")", entry.ID, entry.Category)
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

	// Should have wildlife and bat categories (bird entries are currently hidden)
	require.Contains(t, grouped, CategoryWildlife)
	require.Contains(t, grouped, CategoryBat)

	// All wildlife entries should have the wildlife category
	for _, entry := range grouped[CategoryWildlife] {
		assert.Equal(t, CategoryWildlife, entry.Category)
	}

	// All bat entries should have the bat category
	for _, entry := range grouped[CategoryBat] {
		assert.Equal(t, CategoryBat, entry.Category)
	}

	// Verify expected counts (1 visible wildlife, 0 visible bird, 11 bat entries)
	assert.Len(t, grouped[CategoryWildlife], 1, "expected 1 visible wildlife catalog entry")
	assert.Empty(t, grouped[CategoryBird], "expected 0 visible bird catalog entries")
	assert.Len(t, grouped[CategoryBat], 11, "expected 11 bat catalog entries")
}

func TestEmbeddedCatalog_BatEntriesHaveEmbeddingsFile(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		if entry.Category != CategoryBat {
			continue
		}

		hasEmbeddings := false
		for _, f := range entry.Files {
			if f.Role == RoleEmbeddings {
				hasEmbeddings = true
				assert.Equal(t, "birdnet-v24-embeddings.onnx", f.LocalName,
					"bat entry %q should use shared embeddings file", entry.ID)
				break
			}
		}
		assert.True(t, hasEmbeddings, "bat entry %q must have an embeddings file", entry.ID)
	}
}

func TestEmbeddedCatalog_EntryCount(t *testing.T) {
	t.Parallel()

	// 2 wildlife entries (birdnet-v3.0, perch-v2) + 1 bird entry (bsg-finland) + 11 bat entries = 14 total
	assert.Len(t, EmbeddedCatalog, 14, "expected 14 total catalog entries")
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

	// Visible count should be total minus hidden
	assert.Len(t, visible, len(EmbeddedCatalog)-2)
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
