package audiocore

import (
	"maps"
	"sync"
)

var analysisStateStore = struct {
	mu        sync.RWMutex
	suspended map[string]bool
}{
	suspended: make(map[string]bool),
}

// SetAnalysisSuspended records whether analysis is currently suspended for a source.
func SetAnalysisSuspended(sourceID string, suspended bool) {
	analysisStateStore.mu.Lock()
	defer analysisStateStore.mu.Unlock()
	analysisStateStore.suspended[sourceID] = suspended
}

// RemoveAnalysisState removes the tracked suspension state for a source.
func RemoveAnalysisState(sourceID string) {
	analysisStateStore.mu.Lock()
	defer analysisStateStore.mu.Unlock()
	delete(analysisStateStore.suspended, sourceID)
}

// GetAnalysisSuspendedSnapshot returns a copy of all tracked suspension states.
func GetAnalysisSuspendedSnapshot() map[string]bool {
	analysisStateStore.mu.RLock()
	defer analysisStateStore.mu.RUnlock()
	return maps.Clone(analysisStateStore.suspended)
}
