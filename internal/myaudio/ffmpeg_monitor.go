package myaudio

import (
	"context"
	"sync"
	"time"
)

// ffmpegProcesses is kept for backward compatibility but is no longer used
// The new FFmpegManager handles all process tracking internally
var ffmpegProcesses = &sync.Map{}

// FFmpegMonitor is deprecated and kept only for backward compatibility
// The new FFmpegManager handles all monitoring internally
type FFmpegMonitor struct{}

// NewDefaultFFmpegMonitor creates a stub monitor for backward compatibility
func NewDefaultFFmpegMonitor() *FFmpegMonitor {
	return &FFmpegMonitor{}
}

// Start is a no-op as monitoring is handled by FFmpegManager
func (m *FFmpegMonitor) Start() {
	// No-op - monitoring is handled by FFmpegManager
}

// Stop is a no-op as monitoring is handled by FFmpegManager
func (m *FFmpegMonitor) Stop() {
	// No-op - monitoring is handled by FFmpegManager
}

// IsRunning always returns false as this is just a stub
func (m *FFmpegMonitor) IsRunning() bool {
	return false
}

// MonitorFFmpegProcesses provides backward compatibility for process monitoring
func MonitorFFmpegProcesses(ctx context.Context, interval time.Duration) {
	// This is now handled internally by FFmpegManager
	// Just sync with config periodically
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Sync with configuration to handle removed streams
			_ = SyncRTSPStreamsWithConfig(nil) // Intentionally ignore errors in monitoring loop
		}
	}
}