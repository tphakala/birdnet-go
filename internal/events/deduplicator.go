package events

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
	
	"log/slog"
)

// DeduplicationConfig holds configuration for error deduplication
type DeduplicationConfig struct {
	Enabled         bool
	TTL             time.Duration
	MaxEntries      int
	CleanupInterval time.Duration
}

// DefaultDeduplicationConfig returns default deduplication settings
func DefaultDeduplicationConfig() *DeduplicationConfig {
	return &DeduplicationConfig{
		Enabled:         true,
		TTL:             5 * time.Minute,
		MaxEntries:      10000,
		CleanupInterval: 1 * time.Minute,
	}
}

// ErrorDeduplicator prevents duplicate errors from being processed
type ErrorDeduplicator struct {
	config     *DeduplicationConfig
	cache      map[uint64]*dedupeEntry
	mu         sync.RWMutex
	
	// LRU tracking
	entries    []*lruEntry
	entryMap   map[uint64]int // Maps hash to index in entries slice
	
	// Metrics
	totalSeen      atomic.Uint64
	totalSuppressed atomic.Uint64
	cacheHits      atomic.Uint64
	cacheMisses    atomic.Uint64
	
	// Lifecycle
	stopCleanup chan struct{}
	cleanupDone chan struct{}
	logger      *slog.Logger
}

// dedupeEntry tracks an error occurrence
type dedupeEntry struct {
	hash       uint64
	lastSeen   time.Time
	firstSeen  time.Time
	count      int64
	suppressed int64
}

// lruEntry for LRU eviction
type lruEntry struct {
	hash     uint64
	lastUsed time.Time
}

// NewErrorDeduplicator creates a new error deduplicator
func NewErrorDeduplicator(config *DeduplicationConfig, logger *slog.Logger) *ErrorDeduplicator {
	if config == nil {
		config = DefaultDeduplicationConfig()
	}
	
	ed := &ErrorDeduplicator{
		config:      config,
		cache:       make(map[uint64]*dedupeEntry),
		entries:     make([]*lruEntry, 0, config.MaxEntries),
		entryMap:    make(map[uint64]int),
		stopCleanup: make(chan struct{}),
		cleanupDone: make(chan struct{}),
		logger:      logger,
	}
	
	// Start cleanup goroutine if enabled
	if config.Enabled && config.CleanupInterval > 0 {
		go ed.cleanupLoop()
	}
	
	return ed
}

// ShouldProcess checks if an error should be processed or suppressed
func (ed *ErrorDeduplicator) ShouldProcess(event ErrorEvent) bool {
	if ed == nil || !ed.config.Enabled {
		return true
	}
	
	ed.totalSeen.Add(1)
	
	// Calculate hash for the error
	hash := ed.calculateHash(event)
	
	ed.mu.Lock()
	defer ed.mu.Unlock()
	
	now := time.Now()
	entry, exists := ed.cache[hash]
	
	if !exists {
		// New error, add to cache
		ed.cacheMisses.Add(1)
		
		// Check if we need to evict
		if len(ed.cache) >= ed.config.MaxEntries {
			ed.evictOldest()
		}
		
		// Add new entry
		entry = &dedupeEntry{
			hash:      hash,
			firstSeen: now,
			lastSeen:  now,
			count:     1,
		}
		ed.cache[hash] = entry
		
		// Add to LRU tracking
		lru := &lruEntry{
			hash:     hash,
			lastUsed: now,
		}
		ed.entries = append(ed.entries, lru)
		ed.entryMap[hash] = len(ed.entries) - 1
		
		return true
	}
	
	// Existing error
	ed.cacheHits.Add(1)
	
	// Check if expired
	if now.Sub(entry.lastSeen) > ed.config.TTL {
		// Reset the entry
		entry.firstSeen = now
		entry.lastSeen = now
		entry.count = 1
		entry.suppressed = 0
		
		// Update LRU
		ed.updateLRU(hash, now)
		
		return true
	}
	
	// Duplicate within TTL window
	entry.lastSeen = now
	entry.count++
	entry.suppressed++
	ed.totalSuppressed.Add(1)
	
	// Update LRU
	ed.updateLRU(hash, now)
	
	// Log periodically (every 10 suppressions)
	if entry.suppressed%10 == 0 {
		ed.logger.Debug("suppressing duplicate error",
			"component", event.GetComponent(),
			"category", event.GetCategory(),
			"count", entry.count,
			"suppressed", entry.suppressed,
			"first_seen", entry.firstSeen,
		)
	}
	
	return false
}

// calculateHash generates a hash for error deduplication
func (ed *ErrorDeduplicator) calculateHash(event ErrorEvent) uint64 {
	h := sha256.New()
	
	// Include component and category
	h.Write([]byte(event.GetComponent()))
	h.Write([]byte(event.GetCategory()))
	
	// Include key parts of the error message
	h.Write([]byte(event.GetMessage()))
	
	// Include specific context fields that identify the error
	// (but not timestamps or counters that change)
	ctx := event.GetContext()
	if ctx != nil {
		// Include operation if present
		if op, ok := ctx["operation"].(string); ok {
			h.Write([]byte(op))
		}
		
		// Include error type if present
		if errType, ok := ctx["error_type"].(string); ok {
			h.Write([]byte(errType))
		}
		
		// Include key identifiers but not values that change
		if provider, ok := ctx["provider"].(string); ok {
			h.Write([]byte(provider))
		}
	}
	
	// Convert first 8 bytes to uint64
	sum := h.Sum(nil)
	return binary.BigEndian.Uint64(sum[:8])
}

// updateLRU updates the LRU position of an entry
func (ed *ErrorDeduplicator) updateLRU(hash uint64, now time.Time) {
	if idx, ok := ed.entryMap[hash]; ok {
		ed.entries[idx].lastUsed = now
	}
}

// evictOldest removes the least recently used entry
func (ed *ErrorDeduplicator) evictOldest() {
	if len(ed.entries) == 0 {
		return
	}
	
	// Find oldest entry
	oldestIdx := 0
	oldestTime := ed.entries[0].lastUsed
	
	for i := 1; i < len(ed.entries); i++ {
		if ed.entries[i].lastUsed.Before(oldestTime) {
			oldestIdx = i
			oldestTime = ed.entries[i].lastUsed
		}
	}
	
	// Remove from cache
	oldestHash := ed.entries[oldestIdx].hash
	delete(ed.cache, oldestHash)
	delete(ed.entryMap, oldestHash)
	
	// Remove from entries slice
	ed.entries = append(ed.entries[:oldestIdx], ed.entries[oldestIdx+1:]...)
	
	// Update indices in entryMap
	for i := oldestIdx; i < len(ed.entries); i++ {
		ed.entryMap[ed.entries[i].hash] = i
	}
}

// cleanupLoop periodically removes expired entries
func (ed *ErrorDeduplicator) cleanupLoop() {
	ticker := time.NewTicker(ed.config.CleanupInterval)
	defer ticker.Stop()
	defer close(ed.cleanupDone)
	
	for {
		select {
		case <-ticker.C:
			ed.cleanup()
		case <-ed.stopCleanup:
			return
		}
	}
}

// cleanup removes expired entries
func (ed *ErrorDeduplicator) cleanup() {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	
	now := time.Now()
	expired := 0
	
	// Find expired entries
	var toRemove []uint64
	for hash, entry := range ed.cache {
		if now.Sub(entry.lastSeen) > ed.config.TTL {
			toRemove = append(toRemove, hash)
			expired++
		}
	}
	
	// Remove expired entries
	for _, hash := range toRemove {
		delete(ed.cache, hash)
		
		// Remove from LRU tracking
		if idx, ok := ed.entryMap[hash]; ok {
			// Remove from entries slice
			ed.entries = append(ed.entries[:idx], ed.entries[idx+1:]...)
			delete(ed.entryMap, hash)
			
			// Update indices
			for i := idx; i < len(ed.entries); i++ {
				ed.entryMap[ed.entries[i].hash] = i
			}
		}
	}
	
	if expired > 0 {
		ed.logger.Debug("cleaned up expired deduplication entries",
			"expired", expired,
			"remaining", len(ed.cache),
		)
	}
}

// GetStats returns deduplication statistics
func (ed *ErrorDeduplicator) GetStats() DeduplicationStats {
	if ed == nil {
		return DeduplicationStats{}
	}
	
	ed.mu.RLock()
	cacheSize := len(ed.cache)
	ed.mu.RUnlock()
	
	totalHits := ed.cacheHits.Load()
	totalMisses := ed.cacheMisses.Load()
	hitRate := float64(0)
	
	if total := totalHits + totalMisses; total > 0 {
		hitRate = float64(totalHits) / float64(total) * 100
	}
	
	return DeduplicationStats{
		TotalSeen:       ed.totalSeen.Load(),
		TotalSuppressed: ed.totalSuppressed.Load(),
		CacheSize:       cacheSize,
		CacheHits:       totalHits,
		CacheMisses:     totalMisses,
		HitRate:         hitRate,
	}
}

// Shutdown stops the deduplicator
func (ed *ErrorDeduplicator) Shutdown() {
	if ed == nil {
		return
	}
	
	// Only wait for cleanup if it was started
	if ed.config.Enabled && ed.config.CleanupInterval > 0 {
		close(ed.stopCleanup)
		<-ed.cleanupDone
	}
}

// DeduplicationStats contains deduplication metrics
type DeduplicationStats struct {
	TotalSeen       uint64
	TotalSuppressed uint64
	CacheSize       int
	CacheHits       uint64
	CacheMisses     uint64
	HitRate         float64
}