package imageprovider

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageFileCache_StoreAndGet(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	// Minimal valid JPEG: FFD8FF header triggers image/jpeg detection.
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}

	storedPath, err := cache.Store("wikimedia", "Parus major", jpegData, "https://example.com/img.jpg")
	require.NoError(t, err)
	assert.Contains(t, storedPath, "parus_major.jpg")

	// Read back and verify contents.
	got, err := os.ReadFile(storedPath)
	require.NoError(t, err)
	assert.Equal(t, jpegData, got)

	// Get should find the cached file.
	path, contentType, fresh, err := cache.Get("wikimedia", "Parus major")
	require.NoError(t, err)
	assert.Equal(t, storedPath, path)
	assert.Equal(t, "image/jpeg", contentType)
	assert.True(t, fresh, "newly stored file should be fresh")
}

func TestImageFileCache_GetMiss(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	path, contentType, fresh, err := cache.Get("wikimedia", "Nonexistent species")
	require.NoError(t, err)
	assert.Empty(t, path)
	assert.Empty(t, contentType)
	assert.False(t, fresh)
}

func TestImageFileCache_IsFresh(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	storedPath, err := cache.Store("test", "Turdus merula", jpegData, "")
	require.NoError(t, err)

	// With a 30-day TTL the file should be fresh.
	assert.True(t, cache.IsFresh(storedPath, 30*24*time.Hour))

	// With a zero TTL the file should be stale.
	assert.False(t, cache.IsFresh(storedPath, 0))
}

func TestImageFileCache_NormalizeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple lowercase", input: "parus major", expected: "parus_major"},
		{name: "mixed case", input: "Parus Major", expected: "parus_major"},
		{name: "all caps", input: "PARUS MAJOR", expected: "parus_major"},
		{name: "multiple spaces", input: "Parus  major", expected: "parus__major"},
		{name: "no spaces", input: "turdus", expected: "turdus"},
		{name: "already normalized", input: "parus_major", expected: "parus_major"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, normalizeSpeciesName(tt.input))
		})
	}
}

func TestImageFileCache_RejectsPathTraversal(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))
	data := []byte{0xFF, 0xD8, 0xFF}

	tests := []struct {
		name     string
		provider string
		species  string
	}{
		{name: "dotdot in provider", provider: "../etc", species: "Parus major"},
		{name: "slash in provider", provider: "wiki/evil", species: "Parus major"},
		{name: "backslash in provider", provider: "wiki\\evil", species: "Parus major"},
		{name: "dotdot in species", provider: "wikimedia", species: "../../../etc/passwd"},
		{name: "slash in species", provider: "wikimedia", species: "evil/path"},
		{name: "backslash in species", provider: "wikimedia", species: "evil\\path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := cache.Store(tt.provider, tt.species, data, "")
			require.Error(t, err, "Store should reject path traversal")

			_, _, _, err = cache.Get(tt.provider, tt.species)
			require.Error(t, err, "Get should reject path traversal")
		})
	}
}

func TestImageFileCache_DetectsContentType(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	// PNG file signature.
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

	storedPath, err := cache.Store("wikimedia", "Cyanistes caeruleus", pngData, "https://example.com/img.png")
	require.NoError(t, err)
	assert.Equal(t, ".png", filepath.Ext(storedPath), "expected .png extension")

	path, contentType, _, err := cache.Get("wikimedia", "Cyanistes caeruleus")
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.Equal(t, "image/png", contentType)
}
