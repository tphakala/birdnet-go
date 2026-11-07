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
)

// processingCache manages temporary processed audio files.
type processingCache struct {
	mu       sync.Mutex
	dir      string
	maxFiles int
}

// newProcessingCache creates a cache in the specified directory.
func newProcessingCache(dir string, maxFiles int) *processingCache {
	return &processingCache{dir: dir, maxFiles: maxFiles}
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

// processingCacheKey builds a deterministic filename for cache lookup. The
// sourceTag (see clipSourceTag) ties the entry to the clip file it came from.
func processingCacheKey(detectionID, sourceTag string, normalize bool, denoise string, gainDB float64) string {
	// Sanitize detection ID to prevent path traversal in cache filenames
	safeID := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(detectionID)

	// Canonicalize -0.0 to 0.0
	if gainDB == 0 {
		gainDB = math.Copysign(0, 1) // force positive zero
	}
	norm := "0"
	if normalize {
		norm = "1"
	}
	if denoise == "" {
		denoise = "off"
	}
	return fmt.Sprintf("%s_%s_%s_%s_%.1f.wav", safeID, sourceTag, norm, denoise, gainDB)
}

// audibleBatsCacheKey builds a deterministic filename for a derived "audible
// bats" review clip. The key embeds the time-expansion factor alongside the
// normalize/gain parameters so different settings never collide in the cache,
// and the sourceTag (see clipSourceTag) ties it to the clip file it came from.
func audibleBatsCacheKey(detectionID, sourceTag string, expansion int, normalize bool, gainDB float64) string {
	// Sanitize detection ID to prevent path traversal in cache filenames
	safeID := strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(detectionID)

	// Canonicalize -0.0 to 0.0
	if gainDB == 0 {
		gainDB = math.Copysign(0, 1) // force positive zero
	}
	norm := "0"
	if normalize {
		norm = "1"
	}
	// Full precision (shortest round-trip representation) so distinct gain
	// values never collapse onto the same cache entry, unlike a fixed %.1f.
	gainStr := strconv.FormatFloat(gainDB, 'f', -1, 64)
	return fmt.Sprintf("%s_%s_bat%dx_%s_%s.wav", safeID, sourceTag, expansion, norm, gainStr)
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

// put writes data to cache atomically (temp file + rename), evicting oldest files if over limit.
func (c *processingCache) put(key string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(c.dir, 0o750); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}
	c.evictIfNeeded()

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

// evictIfNeeded removes oldest files if cache exceeds maxFiles.
func (c *processingCache) evictIfNeeded() {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		apicore.GetLogger().Debug("Processing cache: failed to read directory for eviction",
			logger.String("dir", c.dir),
			logger.Error(err))
		return
	}
	if len(entries) < c.maxFiles {
		return
	}

	type fileAge struct {
		path    string
		modTime time.Time
	}
	files := make([]fileAge, 0, len(entries))
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
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	// Remove oldest until under limit
	toRemove := len(files) - c.maxFiles + 1 // make room for the new entry
	if toRemove <= 0 {
		return
	}
	for i := range min(toRemove, len(files)) {
		if err := os.Remove(files[i].path); err != nil {
			apicore.GetLogger().Debug("Processing cache: failed to remove file during eviction",
				logger.String("path", files[i].path),
				logger.Error(err))
		}
	}
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
