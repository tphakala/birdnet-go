package audiocore

import "sync"

var analysisStateStore = struct {
	mu        sync.RWMutex
	suspended map[string]bool
}{
	suspended: make(map[string]bool),
}

func SetAnalysisSuspended(sourceID string, suspended bool) {
	analysisStateStore.mu.Lock()
	defer analysisStateStore.mu.Unlock()
	analysisStateStore.suspended[sourceID] = suspended
}

func RemoveAnalysisState(sourceID string) {
	analysisStateStore.mu.Lock()
	defer analysisStateStore.mu.Unlock()
	delete(analysisStateStore.suspended, sourceID)
}

func GetAnalysisSuspendedSnapshot() map[string]bool {
	analysisStateStore.mu.RLock()
	defer analysisStateStore.mu.RUnlock()
	out := make(map[string]bool, len(analysisStateStore.suspended))
	for sourceID, suspended := range analysisStateStore.suspended {
		out[sourceID] = suspended
	}
	return out
}
