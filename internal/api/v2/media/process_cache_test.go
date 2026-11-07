package media

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cacheKeyPair compares two cache keys: wantEqual says whether they must name
// the same cache entry.
type cacheKeyPair struct {
	name      string
	a, b      string
	wantEqual bool
}

func assertCacheKeyPairs(t *testing.T, pairs []cacheKeyPair) {
	t.Helper()
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			if p.wantEqual {
				assert.Equal(t, p.a, p.b)
			} else {
				assert.NotEqual(t, p.a, p.b)
			}
		})
	}
}

// assertKeyStaysInCacheDir checks that a key built from a hostile detection id
// is a plain file name: no separators and no parent references.
func assertKeyStaysInCacheDir(t *testing.T, key string) {
	t.Helper()
	assert.Equal(t, filepath.Base(key), key, "a detection id must not add path elements")
	assert.NotContains(t, key, "..")
	assert.NotContains(t, key, `\`)
}

func TestClipSourceTag(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	tagOf := func(path string, size int64, mod time.Time) string {
		return clipSourceTag(path, fakeFileInfo{size: size, mod: mod})
	}
	ref := tagOf("a/clip.flac", 100, base)

	assert.Equal(t, ref, tagOf("a/clip.flac", 100, base), "the tag is deterministic")
	assert.NotEqual(t, ref, tagOf("b/clip.flac", 100, base), "path separates tags")
	assert.NotEqual(t, ref, tagOf("a/clip.flac", 101, base), "size separates tags")
	assert.NotEqual(t, ref, tagOf("a/clip.flac", 100, base.Add(time.Nanosecond)), "mtime separates tags at nanosecond resolution")
}

// fakeFileInfo is a minimal os.FileInfo for clipSourceTag tests.
type fakeFileInfo struct {
	os.FileInfo
	size int64
	mod  time.Time
}

func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) ModTime() time.Time { return f.mod }

func TestProcessingCacheKey(t *testing.T) {
	t.Parallel()

	negZero := math.Copysign(0, -1)
	assertCacheKeyPairs(t, []cacheKeyPair{
		{"negative zero gain is zero", processingCacheKey("1", "tag", false, "", 0), processingCacheKey("1", "tag", false, "", negZero), true},
		{"normalize separates entries", processingCacheKey("1", "tag", true, "medium", 6), processingCacheKey("1", "tag", false, "medium", 6), false},
		{"denoise separates entries", processingCacheKey("1", "tag", true, "medium", 6), processingCacheKey("1", "tag", true, "heavy", 6), false},
		{"gain separates entries", processingCacheKey("1", "tag", true, "medium", 6), processingCacheKey("1", "tag", true, "medium", 3), false},
		{"detection id separates entries", processingCacheKey("1", "tag", true, "medium", 6), processingCacheKey("2", "tag", true, "medium", 6), false},
		{"clip source tag separates entries", processingCacheKey("1", "tagA", true, "medium", 6), processingCacheKey("1", "tagB", true, "medium", 6), false},
	})

	t.Run("detection id cannot add path elements", func(t *testing.T) {
		t.Parallel()
		assertKeyStaysInCacheDir(t, processingCacheKey(`..\..\x/y`, "tag", false, "", 0))
	})
}

func TestAudibleBatsCacheKey(t *testing.T) {
	t.Parallel()

	negZero := math.Copysign(0, -1)
	assertCacheKeyPairs(t, []cacheKeyPair{
		{"negative zero gain is zero", audibleBatsCacheKey("1", "tag", 10, false, 0), audibleBatsCacheKey("1", "tag", 10, false, negZero), true},
		{"clip source tag separates entries", audibleBatsCacheKey("1", "tagA", 10, false, 0), audibleBatsCacheKey("1", "tagB", 10, false, 0), false},
		{"expansion separates entries", audibleBatsCacheKey("1", "tag", 10, false, 0), audibleBatsCacheKey("1", "tag", 20, false, 0), false},
		{"normalize separates entries", audibleBatsCacheKey("1", "tag", 10, true, 0), audibleBatsCacheKey("1", "tag", 10, false, 0), false},
		// The key keeps the gain at full precision, unlike the processing key.
		{"gain below 0.1 dB separates entries", audibleBatsCacheKey("1", "tag", 10, false, 6.01), audibleBatsCacheKey("1", "tag", 10, false, 6.04), false},
	})

	t.Run("detection id cannot add path elements", func(t *testing.T) {
		t.Parallel()
		assertKeyStaysInCacheDir(t, audibleBatsCacheKey(`..\x/y`, "tag", 10, false, 0))
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

	t.Run("entries past the TTL are not served and are removed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, processingCacheMaxFiles)
		for _, key := range []string{"fresh.wav", "stale.wav", "stale-read.wav"} {
			require.NoError(t, cache.put(key, []byte(key)))
		}
		expired := time.Now().Add(-processingCacheTTL - time.Minute)
		for _, key := range []string{"stale.wav", "stale-read.wav"} {
			require.NoError(t, os.Chtimes(filepath.Join(dir, key), expired, expired))
		}

		// get refuses an expired entry and deletes it.
		assert.Nil(t, cache.get("stale-read.wav"))
		assert.NoFileExists(t, filepath.Join(dir, "stale-read.wav"))

		// cleanExpired deletes expired entries and leaves fresh ones alone.
		cache.cleanExpired()
		assert.NoFileExists(t, filepath.Join(dir, "stale.wav"))
		assert.Equal(t, []byte("fresh.wav"), cache.get("fresh.wav"))
	})

	t.Run("eviction when over limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 3)
		// Fill to capacity
		for i := range 4 {
			err := cache.put(processingCacheKey("det", "tag", false, "", float64(i)), []byte("data"))
			require.NoError(t, err)
		}
		// Should have evicted oldest to make room
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(entries), 3)
	})
}
