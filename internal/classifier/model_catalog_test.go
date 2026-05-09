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
		if entry.RegistryID == "" {
			// Models without a registry mapping (e.g., BSG) are allowed
			// to have an empty RegistryID until their loader is implemented.
			continue
		}
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
			if f.Role == "model" {
				hasModel = true
				break
			}
		}
		assert.True(t, hasModel, "catalog entry %q has no file with role \"model\"", entry.ID)
	}
}

func TestEmbeddedCatalog_ValidCategories(t *testing.T) {
	t.Parallel()

	validCategories := map[string]bool{"bird": true, "bat": true}
	for _, entry := range EmbeddedCatalog {
		assert.True(t, validCategories[entry.Category],
			"catalog entry %q has invalid category %q (must be \"bird\" or \"bat\")", entry.ID, entry.Category)
	}
}

func TestGetCatalogEntry_Found(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok, "expected to find catalog entry battybirdnet-eu")
	assert.Equal(t, "battybirdnet-eu", entry.ID)
	assert.Equal(t, "BattyBirdNET EU", entry.Name)
	assert.Equal(t, "bat", entry.Category)
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

	// Should have both bird and bat categories
	require.Contains(t, grouped, "bird")
	require.Contains(t, grouped, "bat")

	// All bird entries should have category "bird"
	for _, entry := range grouped["bird"] {
		assert.Equal(t, "bird", entry.Category)
	}

	// All bat entries should have category "bat"
	for _, entry := range grouped["bat"] {
		assert.Equal(t, "bat", entry.Category)
	}

	// Verify expected counts (3 bird entries, 11 bat entries)
	assert.Len(t, grouped["bird"], 3, "expected 3 bird catalog entries")
	assert.Len(t, grouped["bat"], 11, "expected 11 bat catalog entries")
}

func TestEmbeddedCatalog_BatEntriesHaveEmbeddingsFile(t *testing.T) {
	t.Parallel()

	for _, entry := range EmbeddedCatalog {
		if entry.Category != "bat" {
			continue
		}

		hasEmbeddings := false
		for _, f := range entry.Files {
			if f.Role == "embeddings" {
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

	// 3 bird entries (birdnet-v3.0, perch-v2, bsg-finland) + 11 bat entries = 14 total
	assert.Len(t, EmbeddedCatalog, 14, "expected 14 total catalog entries")
}
