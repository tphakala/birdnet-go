// Package spectrogram provides test helper functions for testing cache behavior.
// These functions are only available in test builds.
package spectrogram

import "time"

// ClearAudioDurationCache clears all entries from the audio duration cache.
// This is exported for testing purposes to ensure test isolation.
func ClearAudioDurationCache() {
	audioDurationCache.Lock()
	audioDurationCache.entries = make(map[string]*durationCacheEntry)
	audioDurationCache.Unlock()
}

// GetMaxCacheEntries returns the maximum number of cache entries allowed.
// This is exported for testing purposes.
func GetMaxCacheEntries() int {
	return maxCacheEntries
}

// GetAudioDurationCacheSize returns the current number of entries in the cache.
// This is exported for testing purposes.
func GetAudioDurationCacheSize() int {
	audioDurationCache.RLock()
	defer audioDurationCache.RUnlock()
	return len(audioDurationCache.entries)
}

// AddToCacheForTest adds an entry to the cache for testing purposes.
// The timestamp is set to the current time.
func AddToCacheForTest(path string, duration float64, fileSize int64) {
	AddToCacheForTestWithTimestamp(path, duration, fileSize, time.Now())
}

// AddToCacheForTestWithTimestamp adds an entry to the cache with a specific timestamp.
// This is exported for testing purposes.
func AddToCacheForTestWithTimestamp(path string, duration float64, fileSize int64, timestamp time.Time) {
	audioDurationCache.Lock()
	evictOldCacheEntriesLocked()
	audioDurationCache.entries[path] = &durationCacheEntry{
		duration:  duration,
		timestamp: timestamp,
		fileSize:  fileSize,
		modTime:   timestamp,
	}
	audioDurationCache.Unlock()
}

// HasCacheEntry checks if a cache entry exists for the given path.
// This is exported for testing purposes.
func HasCacheEntry(path string) bool {
	audioDurationCache.RLock()
	defer audioDurationCache.RUnlock()
	_, exists := audioDurationCache.entries[path]
	return exists
}

// GetFFmpegFallbackTimeout returns the timeout duration for FFmpeg fallback.
// This is exported for testing purposes.
func GetFFmpegFallbackTimeout() time.Duration {
	return ffmpegFallbackTimeout
}
