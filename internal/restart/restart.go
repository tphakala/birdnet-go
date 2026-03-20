// Package restart provides concurrency-safe coordination for application restarts.
// It supports two restart modes (binary re-exec and container exit) and tracks
// human-readable reasons for why a restart is required.
package restart

import (
	"slices"
	"sync"
	"sync/atomic"
)

// RestartType identifies the kind of restart requested.
type RestartType int32

const (
	// RestartNone means no restart was requested.
	RestartNone RestartType = iota
	// RestartBinary re-execs the current binary in-place.
	RestartBinary
	// RestartContainer exits the process so the container runtime restarts it.
	RestartContainer
)

// restartFlag stores the requested restart type. Only the first SetXxx call wins.
var restartFlag atomic.Int32

// SetBinaryRestart requests a binary restart. Returns false if a restart is already pending.
func SetBinaryRestart() bool {
	return restartFlag.CompareAndSwap(int32(RestartNone), int32(RestartBinary))
}

// SetContainerRestart requests a container restart. Returns false if a restart is already pending.
func SetContainerRestart() bool {
	return restartFlag.CompareAndSwap(int32(RestartNone), int32(RestartContainer))
}

// Requested returns the current restart type (RestartNone if no restart was requested).
func Requested() RestartType {
	return RestartType(restartFlag.Load())
}

// Reset clears the restart flag. For testing only.
func Reset() {
	restartFlag.Store(int32(RestartNone))
	clearReasons()
}

// --- Restart-required tracking ---

var (
	reasonsMu sync.RWMutex
	reasons   []string
)

// MarkRestartRequired records that a restart is needed, with a human-readable reason.
func MarkRestartRequired(reason string) {
	reasonsMu.Lock()
	defer reasonsMu.Unlock()
	if slices.Contains(reasons, reason) {
		return
	}
	reasons = append(reasons, reason)
}

// IsRestartRequired reports whether any restart-requiring change is pending.
func IsRestartRequired() bool {
	reasonsMu.RLock()
	defer reasonsMu.RUnlock()
	return len(reasons) > 0
}

// GetRestartReasons returns a copy of the current restart reasons.
func GetRestartReasons() []string {
	reasonsMu.RLock()
	defer reasonsMu.RUnlock()
	out := make([]string, len(reasons))
	copy(out, reasons)
	return out
}

// clearReasons resets the reasons list.
func clearReasons() {
	reasonsMu.Lock()
	defer reasonsMu.Unlock()
	reasons = nil
}
