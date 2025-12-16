package securefs

import (
	"io/fs"
	"sync"
	"time"
)

// CacheEntry represents a cached result with expiration
type CacheEntry struct {
	value  any
	expiry time.Time
}

// IsExpired checks if the cache entry has expired
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.expiry)
}

// PathCache provides smart memoization for expensive SecureFS operations
type PathCache struct {
	// Cache for filepath.Abs() results - these rarely change
	absPathCache sync.Map // map[string]CacheEntry

	// Cache for ValidateRelativePath results - these are deterministic
	validatePathCache sync.Map // map[string]CacheEntry

	// Cache for IsPathWithinBase results - these are deterministic
	withinBaseCache sync.Map // map[string]CacheEntry

	// Cache for symlink resolution - these can change but usually stable
	symlinkCache sync.Map // map[string]CacheEntry

	// Cache for stat results - these need shorter TTL as files can change
	statCache sync.Map // map[string]CacheEntry

	// Cache TTL configurations
	absPathTTL    time.Duration // Long TTL - abs paths rarely change
	validateTTL   time.Duration // Long TTL - validation is deterministic
	withinBaseTTL time.Duration // Long TTL - base directory rarely changes
	symlinkTTL    time.Duration // Medium TTL - symlinks can change
	statTTL       time.Duration // Short TTL - file stats change frequently
}

// NewPathCache creates a new PathCache with optimized TTL values
func NewPathCache() *PathCache {
	return &PathCache{
		absPathTTL:    5 * time.Minute,  // Absolute paths rarely change
		validateTTL:   10 * time.Minute, // Path validation is deterministic
		withinBaseTTL: 10 * time.Minute, // Base directory checks are stable
		symlinkTTL:    2 * time.Minute,  // Symlinks can change but usually stable
		statTTL:       30 * time.Second, // File stats change most frequently
	}
}

// GetAbsPath gets or computes absolute path with caching
func (pc *PathCache) GetAbsPath(path string, compute func(string) (string, error)) (string, error) {
	// Check cache first
	if entry, ok := pc.absPathCache.Load(path); ok {
		if cacheEntry := entry.(CacheEntry); !cacheEntry.IsExpired() {
			if result, ok := cacheEntry.value.(absPathResult); ok {
				return result.path, result.err
			}
		}
		// Expired entry, remove it
		pc.absPathCache.Delete(path)
	}

	// Compute result
	result, err := compute(path)

	// Only cache successful results - errors should not be cached
	// to allow transient failures to be retried immediately
	if err == nil {
		cacheEntry := CacheEntry{
			value: absPathResult{
				path: result,
				err:  nil,
			},
			expiry: time.Now().Add(pc.absPathTTL),
		}
		pc.absPathCache.Store(path, cacheEntry)
	}

	return result, err
}

// GetValidatePath gets or computes path validation with caching
func (pc *PathCache) GetValidatePath(path string, compute func(string) (string, error)) (string, error) {
	// Check cache first
	if entry, ok := pc.validatePathCache.Load(path); ok {
		if cacheEntry := entry.(CacheEntry); !cacheEntry.IsExpired() {
			if result, ok := cacheEntry.value.(validatePathResult); ok {
				return result.path, result.err
			}
		}
		// Expired entry, remove it
		pc.validatePathCache.Delete(path)
	}

	// Compute result
	result, err := compute(path)

	// Only cache successful results - errors should not be cached
	// to allow transient failures to be retried immediately
	if err == nil {
		cacheEntry := CacheEntry{
			value: validatePathResult{
				path: result,
				err:  nil,
			},
			expiry: time.Now().Add(pc.validateTTL),
		}
		pc.validatePathCache.Store(path, cacheEntry)
	}

	return result, err
}

// GetWithinBase gets or computes path within base check with caching
func (pc *PathCache) GetWithinBase(key string, compute func() (bool, error)) (bool, error) {
	// Check cache first
	if entry, ok := pc.withinBaseCache.Load(key); ok {
		if cacheEntry := entry.(CacheEntry); !cacheEntry.IsExpired() {
			if result, ok := cacheEntry.value.(withinBaseResult); ok {
				return result.within, result.err
			}
		}
		// Expired entry, remove it
		pc.withinBaseCache.Delete(key)
	}

	// Compute result
	within, err := compute()

	// Only cache successful results - errors should not be cached
	// to allow transient failures to be retried immediately
	if err == nil {
		cacheEntry := CacheEntry{
			value: withinBaseResult{
				within: within,
				err:    nil,
			},
			expiry: time.Now().Add(pc.withinBaseTTL),
		}
		pc.withinBaseCache.Store(key, cacheEntry)
	}

	return within, err
}

// GetSymlinkResolution gets or computes symlink resolution with caching
func (pc *PathCache) GetSymlinkResolution(path string, compute func(string) (string, error)) (string, error) {
	// Check cache first
	if entry, ok := pc.symlinkCache.Load(path); ok {
		if cacheEntry := entry.(CacheEntry); !cacheEntry.IsExpired() {
			if result, ok := cacheEntry.value.(symlinkResult); ok {
				return result.resolved, result.err
			}
		}
		// Expired entry, remove it
		pc.symlinkCache.Delete(path)
	}

	// Compute result
	resolved, err := compute(path)

	// Only cache successful results - errors should not be cached
	// to allow transient failures to be retried immediately
	if err == nil {
		cacheEntry := CacheEntry{
			value: symlinkResult{
				resolved: resolved,
				err:      nil,
			},
			expiry: time.Now().Add(pc.symlinkTTL),
		}
		pc.symlinkCache.Store(path, cacheEntry)
	}

	return resolved, err
}

// GetStat gets or computes file stat with caching
func (pc *PathCache) GetStat(path string, compute func(string) (fs.FileInfo, error)) (fs.FileInfo, error) {
	// Check cache first
	if entry, ok := pc.statCache.Load(path); ok {
		if cacheEntry := entry.(CacheEntry); !cacheEntry.IsExpired() {
			if result, ok := cacheEntry.value.(statResult); ok {
				return result.info, result.err
			}
		}
		// Expired entry, remove it
		pc.statCache.Delete(path)
	}

	// Compute result
	info, err := compute(path)

	// Only cache successful results - errors should not be cached
	// to allow transient failures to be retried immediately
	if err == nil {
		cacheEntry := CacheEntry{
			value: statResult{
				info: info,
				err:  nil,
			},
			expiry: time.Now().Add(pc.statTTL),
		}
		pc.statCache.Store(path, cacheEntry)
	}

	return info, err
}

// ClearExpired removes all expired entries from all caches
// This should be called periodically to prevent memory leaks
func (pc *PathCache) ClearExpired() {
	now := time.Now()

	clearMap := func(m *sync.Map) {
		m.Range(func(key, value any) bool {
			if cacheEntry := value.(CacheEntry); cacheEntry.expiry.Before(now) {
				m.Delete(key)
			}
			return true
		})
	}

	clearMap(&pc.absPathCache)
	clearMap(&pc.validatePathCache)
	clearMap(&pc.withinBaseCache)
	clearMap(&pc.symlinkCache)
	clearMap(&pc.statCache)
}

// GetCacheStats returns statistics about cache usage
func (pc *PathCache) GetCacheStats() CacheStats {
	countEntries := func(m *sync.Map) (total, expired int) {
		now := time.Now()
		m.Range(func(key, value any) bool {
			total++
			if cacheEntry := value.(CacheEntry); cacheEntry.expiry.Before(now) {
				expired++
			}
			return true
		})
		return
	}

	stats := CacheStats{}
	stats.AbsPathTotal, stats.AbsPathExpired = countEntries(&pc.absPathCache)
	stats.ValidateTotal, stats.ValidateExpired = countEntries(&pc.validatePathCache)
	stats.WithinBaseTotal, stats.WithinBaseExpired = countEntries(&pc.withinBaseCache)
	stats.SymlinkTotal, stats.SymlinkExpired = countEntries(&pc.symlinkCache)
	stats.StatTotal, stats.StatExpired = countEntries(&pc.statCache)

	return stats
}

// Cache result types to store in cache entries
type absPathResult struct {
	path string
	err  error
}

type validatePathResult struct {
	path string
	err  error
}

type withinBaseResult struct {
	within bool
	err    error
}

type symlinkResult struct {
	resolved string
	err      error
}

type statResult struct {
	info fs.FileInfo
	err  error
}

// CacheStats provides statistics about cache performance
type CacheStats struct {
	AbsPathTotal      int
	AbsPathExpired    int
	ValidateTotal     int
	ValidateExpired   int
	WithinBaseTotal   int
	WithinBaseExpired int
	SymlinkTotal      int
	SymlinkExpired    int
	StatTotal         int
	StatExpired       int
}
