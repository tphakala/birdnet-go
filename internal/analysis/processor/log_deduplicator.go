// log_deduplicator.go
package processor

import (
	"sync"
	"time"
)

// LogState tracks the last logged state for a source to prevent duplicate logging
type LogState struct {
	LastRawCount      int       // Last logged raw results count
	LastFilteredCount int       // Last logged filtered detections count
	LastLogTime       time.Time // Last time we logged for this source
}

// LogDeduplicator handles intelligent log deduplication for high-frequency messages.
// It prevents repetitive logging while maintaining observability through periodic
// health checks and state change detection.
type LogDeduplicator struct {
	states map[string]*LogState
	mu     sync.RWMutex
	config DeduplicationConfig
}

// DeduplicationConfig controls deduplication behavior
type DeduplicationConfig struct {
	HealthCheckInterval time.Duration // How often to log even if no changes
	Enabled             bool          // Whether deduplication is enabled
}

// NewLogDeduplicator creates a new deduplicator with the given configuration
func NewLogDeduplicator(config DeduplicationConfig) *LogDeduplicator {
	// Set default health check interval if not specified
	if config.HealthCheckInterval == 0 {
		config.HealthCheckInterval = 60 * time.Second
	}
	
	return &LogDeduplicator{
		states: make(map[string]*LogState),
		config: config,
	}
}

// ShouldLog determines if a message should be logged based on deduplication rules.
// It returns whether to log and the reason for logging.
// Reasons include: "dedup_disabled", "first_log", "values_changed", "health_check"
func (d *LogDeduplicator) ShouldLog(source string, rawCount, filteredCount int) (shouldLog bool, reason string) {
	// If deduplication is disabled, always log
	if !d.config.Enabled {
		return true, "dedup_disabled"
	}
	
	d.mu.Lock()
	defer d.mu.Unlock()
	
	now := time.Now()
	state, exists := d.states[source]
	
	// First time seeing this source
	if !exists {
		d.states[source] = &LogState{
			LastRawCount:      rawCount,
			LastFilteredCount: filteredCount,
			LastLogTime:       now,
		}
		return true, "first_log"
	}
	
	// Check if values changed
	if state.LastRawCount != rawCount || state.LastFilteredCount != filteredCount {
		state.LastRawCount = rawCount
		state.LastFilteredCount = filteredCount
		state.LastLogTime = now
		return true, "values_changed"
	}
	
	// Check if it's time for a health check
	if now.Sub(state.LastLogTime) >= d.config.HealthCheckInterval {
		state.LastLogTime = now
		return true, "health_check"
	}
	
	// No need to log - it's a duplicate
	return false, ""
}

// Cleanup removes stale entries to prevent unbounded memory growth.
// Call this periodically (e.g., hourly) to remove sources that haven't
// been seen for longer than staleAfter duration.
func (d *LogDeduplicator) Cleanup(staleAfter time.Duration) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	cutoff := time.Now().Add(-staleAfter)
	removed := 0
	
	for source, state := range d.states {
		if state.LastLogTime.Before(cutoff) {
			delete(d.states, source)
			removed++
		}
	}
	
	return removed
}

// Reset clears all deduplication state
func (d *LogDeduplicator) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	d.states = make(map[string]*LogState)
}

// Stats returns current deduplication statistics
func (d *LogDeduplicator) Stats() (sourceCount int, enabled bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	return len(d.states), d.config.Enabled
}