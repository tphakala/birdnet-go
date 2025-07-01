package telemetry

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// SimpleDeduplicator provides basic error deduplication using a map
type SimpleDeduplicator struct {
	mu       sync.RWMutex
	seen     map[string]time.Time
	window   time.Duration
	maxSize  int
}

// NewSimpleDeduplicator creates a new deduplicator
func NewSimpleDeduplicator(window time.Duration, maxSize int) *SimpleDeduplicator {
	return &SimpleDeduplicator{
		seen:    make(map[string]time.Time),
		window:  window,
		maxSize: maxSize,
	}
}

// IsDuplicate checks if an error is a duplicate within the time window
func (d *SimpleDeduplicator) IsDuplicate(key string) bool {
	d.mu.RLock()
	lastSeen, exists := d.seen[key]
	d.mu.RUnlock()
	
	if !exists {
		d.mu.Lock()
		// Double-check after acquiring write lock
		if _, exists := d.seen[key]; !exists {
			// Cleanup old entries if map is too large
			if len(d.seen) >= d.maxSize {
				d.cleanup()
			}
			d.seen[key] = time.Now()
		}
		d.mu.Unlock()
		return false
	}
	
	// Check if within deduplication window
	return time.Since(lastSeen) < d.window
}

// cleanup removes old entries (simplified version)
func (d *SimpleDeduplicator) cleanup() {
	// In production, this would be more sophisticated
	// For benchmark, just clear half the map
	targetDeletions := len(d.seen) / 2
	if targetDeletions == 0 {
		return
	}
	
	// Collect keys to delete first to avoid modifying map during iteration
	keysToDelete := make([]string, 0, targetDeletions)
	count := 0
	for k := range d.seen {
		keysToDelete = append(keysToDelete, k)
		count++
		if count >= targetDeletions {
			break
		}
	}
	
	// Now delete the collected keys
	for _, k := range keysToDelete {
		delete(d.seen, k)
	}
}

// BenchmarkDeduplication measures the performance of error deduplication
func BenchmarkDeduplication(b *testing.B) {
	dedup := NewSimpleDeduplicator(1*time.Minute, 10000)
	
	b.Run("UniqueErrors", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			// Each error is unique
			key := fmt.Sprintf("error_%d", b.N)
			_ = dedup.IsDuplicate(key)
		}
	})
	
	b.Run("DuplicateErrors", func(b *testing.B) {
		// Pre-populate with some errors
		for i := 0; i < 100; i++ {
			dedup.IsDuplicate(fmt.Sprintf("error_%d", i))
		}
		
		b.ReportAllocs()
		b.ResetTimer()
		
		i := 0
		for b.Loop() {
			// Rotate through 100 known errors
			key := fmt.Sprintf("error_%d", i%100)
			_ = dedup.IsDuplicate(key)
			i++
		}
	})
	
	b.Run("MixedErrors", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		i := 0
		for b.Loop() {
			// 80% duplicates, 20% unique
			var key string
			if i%5 == 0 {
				key = fmt.Sprintf("unique_error_%d", b.N)
			} else {
				key = fmt.Sprintf("common_error_%d", i%20)
			}
			_ = dedup.IsDuplicate(key)
			i++
		}
	})
	
	b.Run("ConcurrentAccess", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("error_%d", i%1000)
				_ = dedup.IsDuplicate(key)
				i++
			}
		})
	})
}

// BenchmarkDeduplicationMemory tests memory usage under load
func BenchmarkDeduplicationMemory(b *testing.B) {
	b.Run("LargeCache", func(b *testing.B) {
		dedup := NewSimpleDeduplicator(5*time.Minute, 100000)
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			// Fill the cache with unique errors
			key := fmt.Sprintf("error_%d", b.N)
			_ = dedup.IsDuplicate(key)
		}
		
		b.Logf("Final cache size: %d entries", len(dedup.seen))
	})
}

// BenchmarkHashingStrategies tests different key generation strategies
func BenchmarkHashingStrategies(b *testing.B) {
	err := fmt.Errorf("connection failed to https://api.example.com: timeout after 30s")
	
	b.Run("FullError", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			_ = err.Error() // Use full error as key
		}
	})
	
	b.Run("SimplifiedError", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			// Simplified: remove URLs and numbers
			_ = ScrubMessage(err.Error())
		}
	})
}