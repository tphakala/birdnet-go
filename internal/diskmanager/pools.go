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

	// MaxPoolSize is the maximum number of slices to keep in the pool
	// Default: 100 (prevents unbounded pool growth)
	MaxPoolSize int
}

// DefaultPoolConfig returns the default pool configuration
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		InitialCapacity: 1000,
		MaxPoolCapacity: 10000,
		MaxParseErrors:  100,
		MaxPoolSize:     100,
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

	// PoolSizeLimit tracks how many times pool size limit was hit
	PoolSizeLimit uint64
}

// poolMetricsAtomic holds atomic counters for thread-safe access
type poolMetricsAtomic struct {
	GetCount            atomic.Uint64
	PutCount            atomic.Uint64
	SkipCount           atomic.Uint64
	MaxCapacityObserved atomic.Uint64
	TotalAllocations    atomic.Uint64
	CurrentPoolSize     atomic.Uint64
	PoolSizeLimit       atomic.Uint64
}

var (
	// poolConfig holds the current pool configuration
	poolConfig = DefaultPoolConfig()

	// poolMetrics tracks pool usage statistics
	poolMetrics poolMetricsAtomic

	// fileInfoPool pools FileInfo slices to reduce allocations during directory walks
	fileInfoPool = sync.Pool{
		New: func() any {
			// Pre-allocate with configured capacity
			slice := make([]FileInfo, 0, poolConfig.InitialCapacity)
			poolMetrics.TotalAllocations.Add(uint64(poolConfig.InitialCapacity))
			return &slice
		},
	}
)

// SetPoolConfig updates the pool configuration
// This should be called before any pool operations
func SetPoolConfig(config *PoolConfig) {
	if config == nil {
		config = DefaultPoolConfig()
	}
	poolConfig = config
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
		PoolSizeLimit:       poolMetrics.PoolSizeLimit.Load(),
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
	poolMetrics.PoolSizeLimit.Store(0)
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

	if cap(*ps.slice) > poolConfig.MaxPoolCapacity {
		// Don't pool huge slices to avoid memory bloat
		poolMetrics.SkipCount.Add(1)
		return
	}

	// Check if pool size limit is reached
	currentSize := poolMetrics.CurrentPoolSize.Load()
	if poolConfig.MaxPoolSize > 0 && int(currentSize) >= poolConfig.MaxPoolSize {
		// Pool is at capacity, don't add more items
		poolMetrics.PoolSizeLimit.Add(1)
		poolMetrics.SkipCount.Add(1)
		return
	}

	poolMetrics.PutCount.Add(1)
	poolMetrics.CurrentPoolSize.Add(1) // Increment pool size counter
	*ps.slice = (*ps.slice)[:0]        // Clear the slice
	fileInfoPool.Put(ps.slice)
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
