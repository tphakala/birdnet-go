package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// logDetectionResultsWithDeduplication logs detection processing results with smart deduplication
// to prevent repetitive logging when values don't change. It logs when:
// - First time logging for a source
// - Raw or filtered counts change
// - More than 60 seconds since last log (health check)
func (p *Processor) logDetectionResultsWithDeduplication(source string, rawCount, filteredCount int) {
	const (
		healthCheckInterval = 60 * time.Second // Log every 60 seconds even if no changes
	)

	p.logStateMutex.Lock()
	defer p.logStateMutex.Unlock()

	now := time.Now()
	
	// Get or create log state for this source
	state, exists := p.lastLogState[source]
	if !exists {
		// First time logging for this source
		state = &LogState{
			FirstLog: true,
		}
		p.lastLogState[source] = state
	}

	// Determine if we should log
	shouldLog := false
	reason := ""

	switch {
	case state.FirstLog:
		shouldLog = true
		reason = "first_log"
		state.FirstLog = false
	case state.LastRawCount != rawCount || state.LastFilteredCount != filteredCount:
		shouldLog = true
		reason = "values_changed"
	case now.Sub(state.LastLogTime) >= healthCheckInterval:
		shouldLog = true
		reason = "health_check"
	}

	// Log only if needed
	if shouldLog {
		GetLogger().Info("Detection processing results",
			"source", privacy.SanitizeRTSPUrls(source),
			"raw_results_count", rawCount,
			"filtered_detections_count", filteredCount,
			"log_reason", reason,
			"operation", "process_detections_summary")
		
		// Update state
		state.LastRawCount = rawCount
		state.LastFilteredCount = filteredCount
		state.LastLogTime = now
	}
}