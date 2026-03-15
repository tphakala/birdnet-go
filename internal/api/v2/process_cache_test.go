package api

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessingCacheKey(t *testing.T) {
	t.Parallel()

	t.Run("deterministic key", func(t *testing.T) {
		t.Parallel()
		key1 := processingCacheKey("123", true, "medium", 6.0)
		key2 := processingCacheKey("123", true, "medium", 6.0)
		assert.Equal(t, key1, key2)
	})

	t.Run("negative zero canonicalized", func(t *testing.T) {
		t.Parallel()
		key1 := processingCacheKey("123", false, "", 0.0)
		key2 := processingCacheKey("123", false, "", math.Copysign(0, -1))
		assert.Equal(t, key1, key2)
	})

	t.Run("different params produce different keys", func(t *testing.T) {
		t.Parallel()
		key1 := processingCacheKey("123", true, "medium", 6.0)
		key2 := processingCacheKey("123", false, "medium", 6.0)
		assert.NotEqual(t, key1, key2)
	})
}

func TestProcessingCache(t *testing.T) {
	t.Parallel()

	t.Run("get miss returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 100)
		data := cache.get("nonexistent.wav")
		assert.Nil(t, data)
	})

	t.Run("put then get returns data", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 100)
		err := cache.put("test.wav", []byte("fake audio data"))
		require.NoError(t, err)
		data := cache.get("test.wav")
		assert.Equal(t, []byte("fake audio data"), data)
	})

	t.Run("directory created on put", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "subdir", ".processing-cache")
		cache := newProcessingCache(dir, 100)
		err := cache.put("test.wav", []byte("data"))
		require.NoError(t, err)
		_, err = os.Stat(dir)
		assert.NoError(t, err)
	})

	t.Run("eviction when over limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 3)
		// Fill to capacity
		for i := range 4 {
			err := cache.put(processingCacheKey("det", false, "", float64(i)), []byte("data"))
			require.NoError(t, err)
		}
		// Should have evicted oldest to make room
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(entries), 3)
	})
}
