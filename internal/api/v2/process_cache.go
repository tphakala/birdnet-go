package api

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

// processingCacheKey builds a deterministic filename for cache lookup.
func processingCacheKey(detectionID string, normalize bool, denoise string, gainDB float64) string {
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
	return fmt.Sprintf("%s_%s_%s_%.1f.wav", safeID, norm, denoise, gainDB)
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
		_ = os.Remove(path)
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
	if err != nil || len(entries) < c.maxFiles {
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
	for i := range min(toRemove, len(files)) {
		_ = os.Remove(files[i].path)
	}
}

// cleanExpired removes all files older than TTL.
func (c *processingCache) cleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > processingCacheTTL {
			_ = os.Remove(filepath.Join(c.dir, e.Name()))
		}
	}
}
