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
}

// poolMetricsAtomic holds atomic counters for thread-safe access
type poolMetricsAtomic struct {
	GetCount            atomic.Uint64
	PutCount            atomic.Uint64
	SkipCount           atomic.Uint64
	MaxCapacityObserved atomic.Uint64
	TotalAllocations    atomic.Uint64
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
	}
}

// ResetPoolMetrics resets all pool metrics to zero
func ResetPoolMetrics() {
	poolMetrics.GetCount.Store(0)
	poolMetrics.PutCount.Store(0)
	poolMetrics.SkipCount.Store(0)
	poolMetrics.MaxCapacityObserved.Store(0)
	poolMetrics.TotalAllocations.Store(0)
}

// getFileInfoSlice retrieves a FileInfo slice from the pool
func getFileInfoSlice() *[]FileInfo {
	poolMetrics.GetCount.Add(1)
	slice := fileInfoPool.Get().(*[]FileInfo)
	*slice = (*slice)[:0] // Reset length but keep capacity

	// Track maximum capacity observed
	capacity := uint64(cap(*slice))
	for {
		old := poolMetrics.MaxCapacityObserved.Load()
		if capacity <= old || poolMetrics.MaxCapacityObserved.CompareAndSwap(old, capacity) {
			break
		}
	}

	return slice
}

// putFileInfoSlice returns a FileInfo slice to the pool
func putFileInfoSlice(slice *[]FileInfo) {
	if slice == nil || cap(*slice) > poolConfig.MaxPoolCapacity {
		// Don't pool huge slices to avoid memory bloat
		poolMetrics.SkipCount.Add(1)
		return
	}
	poolMetrics.PutCount.Add(1)
	*slice = (*slice)[:0] // Clear the slice
	fileInfoPool.Put(slice)
}
