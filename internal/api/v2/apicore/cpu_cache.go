package apicore

import (
	"context"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
)

// cpuCacheUpdateInterval is the interval between CPU cache samples.
const cpuCacheUpdateInterval = 2 * time.Second // Interval for CPU cache updates

// CPUCache holds the cached CPU usage data.
type CPUCache struct {
	mu          sync.RWMutex
	cpuPercent  []float64
	lastUpdated time.Time
}

// Global CPU cache instance. It is shared substrate: the background sampler is
// started by the system domain's route initializer, and the cached value is read
// by the system handlers, the diagnostics CPU-load check, the metrics-history
// collector and the facade /health endpoint. It lives in apicore so all of those
// consumers (across the facade and the system package) share one source without
// an import cycle.
var cpuCache = &CPUCache{
	cpuPercent:  []float64{0}, // Initialize with 0 value
	lastUpdated: time.Now(),
}

// UpdateCPUCache updates the cached CPU usage data until the context is cancelled.
func UpdateCPUCache(ctx context.Context) {
	ticker := time.NewTicker(cpuCacheUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Get CPU usage (this will block for 1 second)
			percent, err := cpu.Percent(time.Second, false)
			if err == nil && len(percent) > 0 {
				cpuCache.mu.Lock()
				cpuCache.cpuPercent = percent
				cpuCache.lastUpdated = time.Now()
				cpuCache.mu.Unlock()
			}

			// Wait for next tick or context cancellation
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

// GetCachedCPUUsage returns the cached CPU usage.
func GetCachedCPUUsage() []float64 {
	cpuCache.mu.RLock()
	defer cpuCache.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]float64, len(cpuCache.cpuPercent))
	copy(result, cpuCache.cpuPercent)
	return result
}
