package media

import (
	"bytes"
	"io"
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
		{"small gain does not share the denoise-only entry", processingCacheKey("1", "tag", false, "medium", 0.04), processingCacheKey("1", "tag", false, "medium", 0), false},
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
		{"negative zero gain is zero", audibleBatsCacheKey("1", "tag", 10, false, "", 0), audibleBatsCacheKey("1", "tag", 10, false, "", negZero), true},
		{"clip source tag separates entries", audibleBatsCacheKey("1", "tagA", 10, false, "", 0), audibleBatsCacheKey("1", "tagB", 10, false, "", 0), false},
		{"expansion separates entries", audibleBatsCacheKey("1", "tag", 10, false, "", 0), audibleBatsCacheKey("1", "tag", 20, false, "", 0), false},
		{"denoise preset separates entries", audibleBatsCacheKey("1", "tag", 10, false, "light-medium", 0), audibleBatsCacheKey("1", "tag", 10, false, "", 0), false},
		{"normalize separates entries", audibleBatsCacheKey("1", "tag", 10, true, "", 0), audibleBatsCacheKey("1", "tag", 10, false, "", 0), false},
		// The key keeps the gain at full precision, unlike the processing key.
		{"gain below 0.1 dB separates entries", audibleBatsCacheKey("1", "tag", 10, false, "", 6.01), audibleBatsCacheKey("1", "tag", 10, false, "", 6.04), false},
	})

	t.Run("detection id cannot add path elements", func(t *testing.T) {
		t.Parallel()
		assertKeyStaysInCacheDir(t, audibleBatsCacheKey(`..\x/y`, "tag", 10, false, "", 0))
	})
}

func TestProcessingCache(t *testing.T) {
	t.Parallel()

	t.Run("get miss returns nil", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 100, processingCacheMaxBytes)
		data := cache.get("nonexistent.wav")
		assert.Nil(t, data)
	})

	t.Run("put then get returns data", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, 100, processingCacheMaxBytes)
		err := cache.put("test.wav", []byte("fake audio data"))
		require.NoError(t, err)
		data := cache.get("test.wav")
		assert.Equal(t, []byte("fake audio data"), data)
	})

	t.Run("directory created on put", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "subdir", ".processing-cache")
		cache := newProcessingCache(dir, 100, processingCacheMaxBytes)
		err := cache.put("test.wav", []byte("data"))
		require.NoError(t, err)
		_, err = os.Stat(dir)
		assert.NoError(t, err)
	})

	t.Run("entries past the TTL are not served and are removed", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, processingCacheMaxFiles, processingCacheMaxBytes)
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
		cache := newProcessingCache(dir, 3, processingCacheMaxBytes)
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

	t.Run("eviction honours the byte budget", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		const entrySize = 10
		cache := newProcessingCache(dir, processingCacheMaxFiles, 3*entrySize)
		payload := bytes.Repeat([]byte("x"), entrySize)
		keys := []string{"a.wav", "b.wav", "c.wav", "d.wav"}
		base := time.Now().Add(-time.Minute)
		for i, key := range keys {
			require.NoError(t, cache.put(key, payload))
			// Distinct write times so eviction order is deterministic.
			stamp := base.Add(time.Duration(i) * time.Second)
			require.NoError(t, os.Chtimes(filepath.Join(dir, key), stamp, stamp))
		}
		assert.NoFileExists(t, filepath.Join(dir, "a.wav"), "the oldest entry makes room for the new one")
		for _, key := range keys[1:] {
			assert.FileExists(t, filepath.Join(dir, key))
		}
	})

	t.Run("an entry larger than the byte budget is not stored", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		const budget = 10
		cache := newProcessingCache(dir, processingCacheMaxFiles, budget)
		require.NoError(t, cache.put("small.wav", []byte("small")))

		err := cache.put("big.wav", bytes.Repeat([]byte("x"), budget+1))
		require.Error(t, err)
		assert.NoFileExists(t, filepath.Join(dir, "big.wav"))
		assert.FileExists(t, filepath.Join(dir, "small.wav"), "an oversized entry evicts nothing")
	})

	t.Run("a failed removal is not counted as room", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		const entrySize = 10
		cache := newProcessingCache(dir, processingCacheMaxFiles, 2*entrySize)
		payload := bytes.Repeat([]byte("x"), entrySize)
		base := time.Now().Add(-time.Minute)
		for i, key := range []string{"locked.wav", "next.wav"} {
			require.NoError(t, cache.put(key, payload))
			stamp := base.Add(time.Duration(i) * time.Second)
			require.NoError(t, os.Chtimes(filepath.Join(dir, key), stamp, stamp))
		}
		locked := filepath.Join(dir, "locked.wav")
		cache.removeEvicted = func(name string) error {
			if name == locked {
				return os.ErrPermission
			}
			return os.Remove(name)
		}

		// The oldest entry cannot be removed, so the next oldest goes instead.
		require.NoError(t, cache.put("new.wav", payload))
		assert.FileExists(t, locked)
		assert.NoFileExists(t, filepath.Join(dir, "next.wav"))
		assert.FileExists(t, filepath.Join(dir, "new.wav"))

		// With no removable entry left the new one is not written.
		cache.removeEvicted = func(string) error { return os.ErrPermission }
		require.Error(t, cache.put("refused.wav", payload))
		assert.NoFileExists(t, filepath.Join(dir, "refused.wav"))
	})

	t.Run("open serves a handle, honours the TTL and misses cleanly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cache := newProcessingCache(dir, processingCacheMaxFiles, processingCacheMaxBytes)
		require.NoError(t, cache.put("hit.wav", []byte("payload")))
		require.NoError(t, cache.put("stale.wav", []byte("old")))
		expired := time.Now().Add(-processingCacheTTL - time.Minute)
		require.NoError(t, os.Chtimes(filepath.Join(dir, "stale.wav"), expired, expired))

		f, info, ok := cache.open("hit.wav")
		require.True(t, ok)
		t.Cleanup(func() { _ = f.Close() })
		assert.Equal(t, int64(len("payload")), info.Size())
		got, err := io.ReadAll(f)
		require.NoError(t, err)
		assert.Equal(t, []byte("payload"), got)

		_, _, ok = cache.open("stale.wav")
		assert.False(t, ok, "an entry past the TTL is not opened")
		assert.NoFileExists(t, filepath.Join(dir, "stale.wav"))
		_, _, ok = cache.open("missing.wav")
		assert.False(t, ok)
	})
}

func TestDenoisedSpectrogramCacheKey(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		raw         bool
		freqToken   string
		styleSuffix string
		want        string
	}{
		{name: "bird profile, raw", id: "7", raw: true, want: "spec_7_tag_light-medium_1026_1_bird.png"},
		{name: "bat profile, legend", id: "7", freqToken: "bat-v3", want: "spec_7_tag_light-medium_1026_0_bat-v3.png"},
		{name: "style suffix is appended", id: "7", raw: true, styleSuffix: "-scientific_dark-dr80", want: "spec_7_tag_light-medium_1026_1_bird-scientific_dark-dr80.png"},
		{name: "path separators in the id are neutralised", id: "../a/b", raw: true, want: "spec__" + "_a_b_tag_light-medium_1026_1_bird.png"},
		{name: "path separators in the style suffix are neutralised", id: "7", raw: true, styleSuffix: "-dr../../outside", want: "spec_7_tag_light-medium_1026_1_bird-dr____outside.png"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := denoisedSpectrogramCacheKey(tc.id, "tag", "light-medium", 1026, tc.raw, tc.freqToken, tc.styleSuffix)
			assert.Equal(t, tc.want, got)
		})
	}
}
