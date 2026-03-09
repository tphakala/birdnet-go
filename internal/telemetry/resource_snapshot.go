package telemetry

import (
	"runtime"
	"sync"
	"time"
)

// snapshotCacheTTL controls how long a cached snapshot remains valid.
// Prevents repeated runtime.ReadMemStats STW pauses during error floods.
const snapshotCacheTTL = 5 * time.Second

// ResourceSnapshot holds privacy-safe system resource metrics at error time.
type ResourceSnapshot struct {
	GoroutineCount int     `json:"goroutine_count"`
	HeapAllocMB    float64 `json:"heap_alloc_mb"`
	HeapSysMB      float64 `json:"heap_sys_mb"`
	NumGC          uint32  `json:"num_gc"`
}

var (
	cachedSnapshot   ResourceSnapshot
	cachedSnapshotAt time.Time
	snapshotMu       sync.Mutex
)

// CollectResourceSnapshot gathers privacy-safe resource metrics.
// Results are cached for snapshotCacheTTL to avoid repeated STW pauses
// from runtime.ReadMemStats during error floods.
func CollectResourceSnapshot() ResourceSnapshot {
	snapshotMu.Lock()
	defer snapshotMu.Unlock()

	if time.Since(cachedSnapshotAt) < snapshotCacheTTL {
		return cachedSnapshot
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	cachedSnapshot = ResourceSnapshot{
		GoroutineCount: runtime.NumGoroutine(),
		HeapAllocMB:    float64(m.HeapAlloc) / (1024 * 1024),
		HeapSysMB:      float64(m.HeapSys) / (1024 * 1024),
		NumGC:          m.NumGC,
	}
	cachedSnapshotAt = time.Now()
	return cachedSnapshot
}
