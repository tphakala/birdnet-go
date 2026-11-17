package media

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	processingCacheTTL            = 15 * time.Minute
	processingCacheTickerInterval = 5 * time.Minute
	processingCacheMaxFiles       = 100

	// processingCacheMaxBytes caps the disk the cache may use. The denoised audio
	// GET makes the cache reachable by every play, so the file count alone does not
	// bound it: an extended-capture WAV can reach about 115 MB.
	processingCacheMaxBytes int64 = 512 << 20
)

// processingCache manages temporary processed audio files.
type processingCache struct {
	mu       sync.Mutex
	dir      string
	maxFiles int
	maxBytes int64

	// removeEvicted deletes an entry during eviction. It is os.Remove; tests
	// replace it to make a removal fail.
	removeEvicted func(name string) error
}

// newProcessingCache creates a cache in the specified directory, bounded by a
// file count and a total byte budget.
func newProcessingCache(dir string, maxFiles int, maxBytes int64) *processingCache {
	return &processingCache{dir: dir, maxFiles: maxFiles, maxBytes: maxBytes, removeEvicted: os.Remove}
}

// clipSourceTag identifies the clip file a processed artifact was made from. It
// hashes the normalized clip path, size and modification time (FNV-1a 64, hex),
// so a detection ID reused for a clip at another path, or a clip file replaced
// with one of a different size or mtime, does not match a cache entry made from
// the earlier file. A same-size replacement that keeps or restores the mtime
// (for example a copy that preserves timestamps), or lands within one tick of a
// coarse mtime clock, keeps the tag until the entry expires. The tag depends only
// on that one file, so other detections never move its key.
func clipSourceTag(normalizedPath string, info os.FileInfo) string {
	h := fnv.New64a()
	// Hash writes never fail.
	_, _ = fmt.Fprintf(h, "%s\x00%d\x00%d", normalizedPath, info.Size(), info.ModTime().UnixNano())
	return strconv.FormatUint(h.Sum64(), 16)
}

const (
	// spectrogramCacheKeyPrefix starts the filename of a rendered spectrogram in
	// the processing cache, apart from the processed audio entries.
	spectrogramCacheKeyPrefix = "spec"
	// defaultProfileKeyToken stands in the key for the default bird frequency
	// profile, whose spectrogram.ProfileSuffix is empty.
	defaultProfileKeyToken = "bird"
	// cacheFlagOn and cacheFlagOff encode a boolean in a cache key.
	cacheFlagOn  = "1"
	cacheFlagOff = "0"
)

// sanitizeCacheID makes a detection ID or another key part safe to embed in a
// cache filename (no path separators or parent references).
func sanitizeCacheID(part string) string {
	return strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(part)
}

// processingCacheKey builds a deterministic filename for cache lookup. The
// sourceTag (see clipSourceTag) ties the entry to the clip file it came from, and
// every filter value is kept exactly, so distinct filter sets never share a key.
func processingCacheKey(detectionID, sourceTag string, normalize bool, denoise string, gainDB float64) string {
	// Sanitize detection ID to prevent path traversal in cache filenames
	safeID := sanitizeCacheID(detectionID)

	// Canonicalize -0.0 to 0.0
	if gainDB == 0 {
		gainDB = math.Copysign(0, 1) // force positive zero
	}
	norm := cacheFlagOff
	if normalize {
		norm = cacheFlagOn
	}
	if denoise == "" {
		denoise = "off"
	}
	// Full precision, as in audibleBatsCacheKey: a rounded gain would let a POST
	// with a small nonzero gain share the entry and the job of the denoise-only GET.
	gainStr := strconv.FormatFloat(gainDB, 'f', -1, 64)
	return fmt.Sprintf("%s_%s_%s_%s_%s.wav", safeID, sourceTag, norm, denoise, gainStr)
}

// denoisedSpectrogramCacheKey builds the filename of a rendered spectrogram of a
// denoised clip. Beyond the clip's own tag it carries every value the render
// depends on: preset, width, raw flag, frequency profile token and the style
// suffix (see buildStyleSuffix), so a change of Style or DynamicRange settings
// misses the old entry and renders fresh. The style suffix comes from settings
// values that are not all validated, so it is sanitized like the ID and the key
// stays one filename inside the cache directory.
func denoisedSpectrogramCacheKey(detectionID, sourceTag, preset string, width int, raw bool, freqToken, styleSuffix string) string {
	rawFlag := cacheFlagOff
	if raw {
		rawFlag = cacheFlagOn
	}
	if freqToken == "" {
		freqToken = defaultProfileKeyToken
	}
	return fmt.Sprintf("%s_%s_%s_%s_%d_%s_%s%s.png", spectrogramCacheKeyPrefix,
		sanitizeCacheID(detectionID), sourceTag, preset, width, rawFlag, freqToken, sanitizeCacheID(styleSuffix))
}

// audibleBatsCacheKey builds a deterministic filename for a derived "audible
// bats" review clip. The key embeds the time-expansion factor alongside the
// denoise/normalize/gain parameters so different settings never collide in the cache,
// and the sourceTag (see clipSourceTag) ties it to the clip file it came from.
func audibleBatsCacheKey(detectionID, sourceTag string, expansion int, normalize bool, denoise string, gainDB float64) string {
	// Sanitize detection ID to prevent path traversal in cache filenames
	safeID := sanitizeCacheID(detectionID)

	// Canonicalize -0.0 to 0.0
	if gainDB == 0 {
		gainDB = math.Copysign(0, 1) // force positive zero
	}
	norm := cacheFlagOff
	if normalize {
		norm = cacheFlagOn
	}
	if denoise == "" {
		denoise = "off"
	}
	// Full precision (shortest round-trip representation) so distinct gain
	// values never collapse onto the same cache entry, unlike a fixed %.1f.
	gainStr := strconv.FormatFloat(gainDB, 'f', -1, 64)
	return fmt.Sprintf("%s_%s_bat%dx_%s_%s_%s.wav", safeID, sourceTag, expansion, norm, denoise, gainStr)
}

// get returns cached file data or nil if not found / expired.
func (c *processingCache) get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.dir, key)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if time.Since(info.ModTime()) > processingCacheTTL {
		if err := os.Remove(path); err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to remove expired entry on read",
				logger.String("path", path),
				logger.Error(err))
		}
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: path derived from controlled cache key
	if err != nil {
		return nil
	}
	return data
}

// open returns an open handle and the file info of a cached entry, or false when
// it is missing or expired. It serves hits without reading the whole file into
// memory. The caller closes the handle.
func (c *processingCache) open(key string) (*os.File, os.FileInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.dir, key)
	f, err := os.Open(path) //nolint:gosec // G304: path derived from controlled cache key
	if err != nil {
		return nil, nil, false
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, false
	}
	if time.Since(info.ModTime()) > processingCacheTTL {
		_ = f.Close()
		if err := os.Remove(path); err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to remove expired entry on open",
				logger.String("path", path),
				logger.Error(err))
		}
		return nil, nil, false
	}
	return f, info, true
}

// entryModTime returns the write time of a cached entry, or false when it is
// missing.
func (c *processingCache) entryModTime(key string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	info, err := os.Stat(filepath.Join(c.dir, key))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// put writes data to cache atomically (temp file + rename), evicting oldest files
// to stay under both limits. It stores nothing, and returns an error, when data
// alone exceeds the byte budget or eviction could not make room.
func (c *processingCache) put(key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	incoming := int64(len(data))
	if incoming > c.maxBytes {
		return fmt.Errorf("entry of %d bytes exceeds the cache budget of %d bytes", incoming, c.maxBytes)
	}
	if err := os.MkdirAll(c.dir, 0o750); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}
	if !c.evictIfNeeded(incoming) {
		return fmt.Errorf("no room for a %d byte entry: older entries could not be removed", incoming)
	}

	// Write to temp file first, then rename for atomicity
	tmpFile, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp cache file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to write cache data: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close cache temp file: %w", err)
	}

	finalPath := filepath.Join(c.dir, key)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename cache file: %w", err)
	}
	return nil
}

// evictIfNeeded removes oldest files until the cache has room for one more entry
// of incoming bytes, under both the file-count and the byte budget. It reports
// whether the entry fits. A file whose removal fails (for example one still open
// for serving on Windows) stays counted, and eviction moves on to the next oldest.
func (c *processingCache) evictIfNeeded(incoming int64) bool {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		apicore.GetLogger().Debug("Processing cache: failed to read directory for eviction",
			logger.String("dir", c.dir),
			logger.Error(err))
		return false
	}
	type fileAge struct {
		path    string
		modTime time.Time
		size    int64
	}
	files := make([]fileAge, 0, len(entries))
	var totalBytes int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to stat entry during eviction",
				logger.String("name", e.Name()),
				logger.Error(err))
			continue
		}
		files = append(files, fileAge{
			path:    filepath.Join(c.dir, e.Name()),
			modTime: info.ModTime(),
			size:    info.Size(),
		})
		totalBytes += info.Size()
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Remove oldest until the new entry fits under both limits.
	remaining := len(files)
	fits := func() bool { return remaining < c.maxFiles && totalBytes+incoming <= c.maxBytes }
	for _, f := range files {
		if fits() {
			break
		}
		if err := c.removeEvicted(f.path); err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to remove file during eviction",
				logger.String("path", f.path),
				logger.Error(err))
			continue
		}
		remaining--
		totalBytes -= f.size
	}
	return fits()
}

// cleanExpired removes all files older than TTL.
func (c *processingCache) cleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		apicore.GetLogger().Debug("Processing cache: failed to read directory for cleanup",
			logger.String("dir", c.dir),
			logger.Error(err))
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to stat entry during cleanup",
				logger.String("name", e.Name()),
				logger.Error(err))
			continue
		}
		if time.Since(info.ModTime()) > processingCacheTTL {
			if err := os.Remove(filepath.Join(c.dir, e.Name())); err != nil {
				apicore.GetLogger().Debug("Processing cache: failed to remove expired file",
					logger.String("name", e.Name()),
					logger.Error(err))
			}
		}
	}
}
