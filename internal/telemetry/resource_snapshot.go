package telemetry

import "runtime"

// ResourceSnapshot holds privacy-safe system resource metrics at error time.
type ResourceSnapshot struct {
	GoroutineCount int     `json:"goroutine_count"`
	HeapAllocMB    float64 `json:"heap_alloc_mb"`
	HeapSysMB      float64 `json:"heap_sys_mb"`
	NumGC          uint32  `json:"num_gc"`
}

// CollectResourceSnapshot gathers privacy-safe resource metrics.
func CollectResourceSnapshot() ResourceSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ResourceSnapshot{
		GoroutineCount: runtime.NumGoroutine(),
		HeapAllocMB:    float64(m.HeapAlloc) / (1024 * 1024),
		HeapSysMB:      float64(m.HeapSys) / (1024 * 1024),
		NumGC:          m.NumGC,
	}
}
