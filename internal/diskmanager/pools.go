// pools.go - memory pools for reducing allocations
package diskmanager

import (
	"sync"
	"sync/atomic"
)

// PoolConfig holds configuration for memory pools
type PoolConfig struct {
	// InitialCapacity is the initial capacity for pooled slices
	// Default: 1000
	InitialCapacity int

	// MaxPoolCapacity is the maximum capacity for slices to be returned to pool
	// Slices larger than this will not be pooled to avoid memory bloat
	// Default: 10000
	MaxPoolCapacity int

	// MaxParseErrors is the maximum number of parse errors to track
	// Default: 100
	MaxParseErrors int
}

// DefaultPoolConfig returns the default pool configuration
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		InitialCapacity: 1000,
		MaxPoolCapacity: 10000,
		MaxParseErrors:  100,
	}
}

// PoolMetrics tracks pool usage statistics
type PoolMetrics struct {
	// GetCount tracks number of pool Get operations
	GetCount uint64

	// PutCount tracks number of pool Put operations
	PutCount uint64

	// SkipCount tracks number of times Put was skipped due to size
	SkipCount uint64

	// MaxCapacityObserved tracks the largest slice capacity seen
	MaxCapacityObserved uint64

	// TotalAllocations tracks total memory allocated (in FileInfo entries)
	TotalAllocations uint64

	// CurrentPoolSize tracks the current number of items in the pool
	CurrentPoolSize uint64
}

// poolMetricsAtomic holds atomic counters for thread-safe access
type poolMetricsAtomic struct {
	GetCount            atomic.Uint64
	PutCount            atomic.Uint64
	SkipCount           atomic.Uint64
	MaxCapacityObserved atomic.Uint64
	TotalAllocations    atomic.Uint64
	CurrentPoolSize     atomic.Uint64
}

var (
	// poolConfig holds the current pool configuration (thread-safe)
	poolConfig atomic.Pointer[PoolConfig]

	// poolMetrics tracks pool usage statistics
	poolMetrics poolMetricsAtomic

	// fileInfoPool pools FileInfo slices to reduce allocations during directory walks
	fileInfoPool = sync.Pool{
		New: func() any {
			// Pre-allocate with configured capacity
			cfg := loadPoolConfig()
			slice := make([]FileInfo, 0, cfg.InitialCapacity)
			poolMetrics.TotalAllocations.Add(uint64(cfg.InitialCapacity))
			return &slice
		},
	}
)

func init() {
	// Initialize with default config
	poolConfig.Store(DefaultPoolConfig())
}

// SetPoolConfig updates the pool configuration thread-safely.
// Creates a defensive copy and validates values to prevent external mutation.
// This should ideally be called during initialization before heavy pool usage.
func SetPoolConfig(config *PoolConfig) {
	if config == nil {
		config = DefaultPoolConfig()
	}

	// Create defensive copy to prevent external mutation
	newConfig := &PoolConfig{
		InitialCapacity: clampInt(config.InitialCapacity, 100, 100000),
		MaxPoolCapacity: clampInt(config.MaxPoolCapacity, 1000, 1000000),
		MaxParseErrors:  clampInt(config.MaxParseErrors, 10, 10000),
	}

	poolConfig.Store(newConfig)
}

// loadPoolConfig returns the current pool configuration thread-safely
func loadPoolConfig() *PoolConfig {
	return poolConfig.Load()
}

// clampInt constrains a value between minVal and maxVal
func clampInt(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

// GetPoolMetrics returns a copy of current pool metrics
func GetPoolMetrics() PoolMetrics {
	return PoolMetrics{
		GetCount:            poolMetrics.GetCount.Load(),
		PutCount:            poolMetrics.PutCount.Load(),
		SkipCount:           poolMetrics.SkipCount.Load(),
		MaxCapacityObserved: poolMetrics.MaxCapacityObserved.Load(),
		TotalAllocations:    poolMetrics.TotalAllocations.Load(),
		CurrentPoolSize:     poolMetrics.CurrentPoolSize.Load(),
	}
}

// ResetPoolMetrics resets all pool metrics to zero
func ResetPoolMetrics() {
	poolMetrics.GetCount.Store(0)
	poolMetrics.PutCount.Store(0)
	poolMetrics.SkipCount.Store(0)
	poolMetrics.MaxCapacityObserved.Store(0)
	poolMetrics.TotalAllocations.Store(0)
	poolMetrics.CurrentPoolSize.Store(0)
}

// PooledSlice wraps a pooled slice with ownership management
type PooledSlice struct {
	slice    *[]FileInfo
	returned bool // Prevents double-return to pool
}

// getPooledSlice retrieves a FileInfo slice from the pool wrapped with ownership management
func getPooledSlice() *PooledSlice {
	poolMetrics.GetCount.Add(1)
	slice := fileInfoPool.Get().(*[]FileInfo)
	*slice = (*slice)[:0] // Reset length but keep capacity

	// Decrement pool size counter (item was removed from pool)
	if current := poolMetrics.CurrentPoolSize.Load(); current > 0 {
		poolMetrics.CurrentPoolSize.Add(^uint64(0)) // Decrement by 1
	}

	// Track maximum capacity observed
	capacity := uint64(cap(*slice))
	for {
		old := poolMetrics.MaxCapacityObserved.Load()
		if capacity <= old || poolMetrics.MaxCapacityObserved.CompareAndSwap(old, capacity) {
			break
		}
	}

	return &PooledSlice{
		slice:    slice,
		returned: false,
	}
}

// Release returns the slice to the pool (if not already returned)
func (ps *PooledSlice) Release() {
	if ps.returned || ps.slice == nil {
		return
	}
	ps.returned = true

	cfg := loadPoolConfig()
	if cap(*ps.slice) > cfg.MaxPoolCapacity {
		// Don't pool huge slices to avoid memory bloat
		poolMetrics.SkipCount.Add(1)

		// Clear references to prevent memory leaks (Go 1.21+ clear function)
		// Even though we're not pooling, we must drop all references
		clear(*ps.slice)            // Zero out all elements
		*ps.slice = (*ps.slice)[:0] // Reset length to 0
		ps.slice = nil              // Drop the slice reference
		return
	}

	// Always put back to pool - let sync.Pool manage its own size
	// sync.Pool is designed as a lossy cache that responds to memory pressure
	poolMetrics.PutCount.Add(1)
	poolMetrics.CurrentPoolSize.Add(1) // Increment pool size counter

	// Drop references before pooling (Go 1.21+)
	clear(*ps.slice)            // Zero used elements
	*ps.slice = (*ps.slice)[:0] // Reset length

	fileInfoPool.Put(ps.slice)
	ps.slice = nil // Clear the reference from PooledSlice
}

// TakeOwnership transfers ownership of the data and releases the pooled slice
// Returns a new slice with the data, caller owns the returned slice
func (ps *PooledSlice) TakeOwnership() []FileInfo {
	if ps.slice == nil {
		return nil
	}

	// Create a new slice with exact capacity
	result := make([]FileInfo, len(*ps.slice))
	copy(result, *ps.slice)

	// Release the pooled slice back to the pool
	ps.Release()

	return result
}

// Data returns a reference to the underlying slice for appending
// Caller must not retain this reference after Release or TakeOwnership
func (ps *PooledSlice) Data() *[]FileInfo {
	return ps.slice
}

// SetData replaces the contents while preserving the pooled backing array
// This properly reuses the pooled memory instead of allocating new slices
func (ps *PooledSlice) SetData(data []FileInfo) {
	if ps.slice == nil {
		return
	}
	// Reuse the backing array by appending into the cleared slice
	*ps.slice = append((*ps.slice)[:0], data...)

	// Track capacity growth if append reallocated
	if newCap := uint64(cap(*ps.slice)); newCap > poolMetrics.MaxCapacityObserved.Load() {
		for {
			old := poolMetrics.MaxCapacityObserved.Load()
			if newCap <= old || poolMetrics.MaxCapacityObserved.CompareAndSwap(old, newCap) {
				break
			}
		}
	}
}
