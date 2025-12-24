# SecureFS Smart Memoization

## Overview

SecureFS now includes intelligent caching to eliminate performance bottlenecks in the V2 JSON API. The smart memoization system caches expensive filesystem operations that were causing significant overhead.

## Performance Improvements

### Real-World Spectrogram Generation Scenario

```
BenchmarkRealWorldSpectrogramScenario/WithoutCache-16    31,308 ns/op   16,640 B/op   185 allocs/op
BenchmarkRealWorldSpectrogramScenario/WithCache-16        2,347 ns/op      960 B/op    10 allocs/op
```

**13.3x performance improvement** with **17.3x reduction in memory allocations**

### Repeated Operations Scenario

```
BenchmarkRepeatedStatOperationsWithoutCache-16          21,515 ns/op   10,176 B/op   131 allocs/op
BenchmarkRepeatedStatOperationsWithCache-16              2,477 ns/op      768 B/op    15 allocs/op
```

**8.7x performance improvement** with **8.7x reduction in memory allocations**

## What Gets Cached

The caching system optimizes these expensive operations:

1. **Absolute Path Resolution** (`filepath.Abs`)
   - TTL: 5 minutes
   - High impact on performance

2. **Path Validation** (`ValidateRelativePath`)
   - TTL: 10 minutes
   - Deterministic results, safe to cache long-term

3. **Path Within Base Checks** (`IsPathWithinBase`)
   - TTL: 10 minutes
   - Base directory rarely changes

4. **Symlink Resolution** (`filepath.EvalSymlinks`)
   - TTL: 2 minutes
   - Can change but usually stable

5. **File Stat Operations** (`os.Stat`)
   - TTL: 30 seconds
   - Shortest TTL as file metadata changes frequently

## Cache Management

### Automatic Cleanup

```go
// Start background cleanup (recommended)
stopCh := sfs.StartCacheCleanup(5 * time.Minute)

// Stop cleanup when shutting down
close(stopCh)
```

### Manual Cache Management

```go
// Clear expired entries manually
sfs.ClearExpiredCache()

// Get cache statistics for monitoring
stats := sfs.GetCacheStats()
fmt.Printf("Validate cache: %d entries (%d expired)\n",
    stats.ValidateTotal, stats.ValidateExpired)
```

### Cache Statistics

```go
type CacheStats struct {
    AbsPathTotal      int // Absolute path cache entries
    AbsPathExpired    int
    ValidateTotal     int // Path validation cache entries
    ValidateExpired   int
    WithinBaseTotal   int // Path within base cache entries
    WithinBaseExpired int
    SymlinkTotal      int // Symlink resolution cache entries
    SymlinkExpired    int
    StatTotal         int // File stat cache entries
    StatExpired       int
}
```

## Root Cause Analysis

The V2 JSON API had performance bottlenecks due to:

1. **Multiple `filepath.Abs()` calls** - Expensive system calls
2. **Symlink resolution loops** - `filepath.EvalSymlinks()` with recursive traversal
3. **Repeated path validation** - Complex string operations for the same paths
4. **Frequent stat operations** - Multiple filesystem calls for identical files

## Architecture

The smart memoization uses:

- **Thread-safe caching** with `sync.Map` for concurrent access
- **TTL-based expiration** with different lifetimes for different operation types
- **Configurable cache policies** optimized for each operation type
- **Memory-efficient storage** with automatic cleanup of expired entries

## Usage

SecureFS automatically uses caching - no code changes required. The cache is initialized when creating a new SecureFS instance:

```go
sfs, err := securefs.New("/path/to/base/directory")
if err != nil {
    return err
}

// Cache is automatically enabled and ready to use
// All SecureFS operations now benefit from smart memoization
```

## Memory Impact

The cache uses minimal memory overhead:

- Small cache entries (typically <100 bytes each)
- Automatic expiration prevents unbounded growth
- Background cleanup removes expired entries
- Significant reduction in overall allocations

## Thread Safety

All cache operations are thread-safe and designed for high-concurrency scenarios like web servers handling multiple simultaneous requests.
