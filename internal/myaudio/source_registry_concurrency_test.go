// source_registry_concurrency_test.go - Tests for concurrent operations and security validation
package myaudio

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRaceConditionFix verifies that MigrateSourceAtomic prevents race conditions
func TestRaceConditionFix(t *testing.T) {
	registry := &AudioSourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Test concurrent migrations of the same source
	const numGoroutines = 100
	const testURL = "rtsp://race-test.local/stream"
	
	results := make(chan string, numGoroutines)
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			id := registry.MigrateSourceAtomic(testURL, SourceTypeRTSP)
			results <- id
		}()
	}

	wg.Wait()
	close(results)

	// Collect all returned IDs
	ids := make(map[string]int)
	for id := range results {
		ids[id]++
	}

	// All goroutines should have returned the same ID
	if len(ids) != 1 {
		t.Errorf("Race condition detected: got %d different IDs, expected 1", len(ids))
		for id, count := range ids {
			t.Logf("ID %s returned %d times", id, count)
		}
	}

	// Verify only one source was created
	if len(registry.sources) != 1 {
		t.Errorf("Expected 1 source in registry, got %d", len(registry.sources))
	}
}

// TestMemoryLeakFix verifies that sources can be properly cleaned up
func TestMemoryLeakFix(t *testing.T) {
	registry := &AudioSourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Register multiple sources
	urls := []string{
		"rtsp://cam1.local/stream",
		"rtsp://cam2.local/stream", 
		"rtsp://cam3.local/stream",
	}

	sourceIDs := make([]string, 0, len(urls))
	for _, url := range urls {
		source, err := registry.RegisterSource(url, SourceConfig{
			Type: SourceTypeRTSP,
		})
		if err != nil {
			t.Fatalf("Failed to register source: %v", err)
		}
		sourceIDs = append(sourceIDs, source.ID)
	}

	// Verify sources are registered
	if len(registry.sources) != 3 {
		t.Errorf("Expected 3 sources, got %d", len(registry.sources))
	}

	// Remove sources
	for _, id := range sourceIDs {
		if err := registry.RemoveSource(id); err != nil {
			t.Errorf("Failed to remove source %s: %v", id, err)
		}
	}

	// Verify sources are removed
	if len(registry.sources) != 0 {
		t.Errorf("Expected 0 sources after cleanup, got %d", len(registry.sources))
	}
	if len(registry.connectionMap) != 0 {
		t.Errorf("Expected 0 entries in connectionMap after cleanup, got %d", len(registry.connectionMap))
	}
}

// TestInactiveSourceCleanup verifies that inactive sources can be cleaned up
func TestInactiveSourceCleanup(t *testing.T) {
	registry := &AudioSourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}

	// Register sources with different last seen times
	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)

	// Active source
	activeSource := &AudioSource{
		ID:               "active_001",
		connectionString: "rtsp://active.local/stream",
		IsActive:         true,
		LastSeen:         now,
	}
	registry.sources[activeSource.ID] = activeSource
	registry.connectionMap[activeSource.connectionString] = activeSource.ID

	// Inactive but recent source
	recentSource := &AudioSource{
		ID:               "recent_001",
		connectionString: "rtsp://recent.local/stream",
		IsActive:         false,
		LastSeen:         now.Add(-30 * time.Minute),
	}
	registry.sources[recentSource.ID] = recentSource
	registry.connectionMap[recentSource.connectionString] = recentSource.ID

	// Inactive old source
	oldSource := &AudioSource{
		ID:               "old_001",
		connectionString: "rtsp://old.local/stream",
		IsActive:         false,
		LastSeen:         oldTime,
	}
	registry.sources[oldSource.ID] = oldSource
	registry.connectionMap[oldSource.connectionString] = oldSource.ID

	// Clean up sources inactive for more than 1 hour
	removed := registry.CleanupInactiveSources(1 * time.Hour)

	// Should remove only the old inactive source
	if removed != 1 {
		t.Errorf("Expected to remove 1 source, removed %d", removed)
	}

	// Verify correct sources remain
	if _, exists := registry.sources["active_001"]; !exists {
		t.Error("Active source should not be removed")
	}
	if _, exists := registry.sources["recent_001"]; !exists {
		t.Error("Recent inactive source should not be removed")
	}
	if _, exists := registry.sources["old_001"]; exists {
		t.Error("Old inactive source should be removed")
	}
}

// TestURLValidation verifies that dangerous URLs are rejected
func TestURLValidation(t *testing.T) {
	registry := GetRegistry()

	testCases := []struct {
		name        string
		url         string
		sourceType  SourceType
		shouldFail  bool
		errorContains string
	}{
		{
			name:       "Valid RTSP URL",
			url:        "rtsp://192.168.1.100/stream",
			sourceType: SourceTypeRTSP,
			shouldFail: false,
		},
		{
			name:       "RTSP with credentials",
			url:        "rtsp://user:pass@192.168.1.100/stream",
			sourceType: SourceTypeRTSP,
			shouldFail: false,
		},
		{
			name:       "Command injection attempt",
			url:        "rtsp://test.com/stream; rm -rf /",
			sourceType: SourceTypeRTSP,
			shouldFail: true, // Semicolons are rejected for security - this is command injection
		},
		{
			name:       "Shell variable injection",
			url:        "rtsp://test.com/stream$(whoami)",
			sourceType: SourceTypeRTSP,
			shouldFail: true,
			errorContains: "dangerous pattern",
		},
		{
			name:       "Pipe injection",
			url:        "test.wav | cat /etc/passwd",
			sourceType: SourceTypeFile,
			shouldFail: true,
			errorContains: "dangerous pattern",
		},
		{
			name:       "Valid audio device",
			url:        "hw:1,0",
			sourceType: SourceTypeAudioCard,
			shouldFail: false,
		},
		{
			name:       "Invalid audio device",
			url:        "/dev/null",
			sourceType: SourceTypeAudioCard,
			shouldFail: true,
			errorContains: "invalid audio device",
		},
		{
			name:       "Directory traversal in file",
			url:        "../../../etc/passwd",
			sourceType: SourceTypeFile,
			shouldFail: true,
			errorContains: "directory traversal",
		},
		{
			name:       "Test scheme for testing",
			url:        "test://health-check-loop",
			sourceType: SourceTypeRTSP,
			shouldFail: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.RegisterSource(tc.url, SourceConfig{
				Type: tc.sourceType,
			})

			if tc.shouldFail {
				if err == nil {
					t.Errorf("Expected validation to fail for %s", tc.url)
				} else if tc.errorContains != "" && !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %v", tc.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected validation to pass for %s, got: %v", tc.url, err)
				}
			}
		})
	}
}

// TestConcurrentMigrationAndCleanup tests that migration and cleanup don't race
func TestConcurrentMigrationAndCleanup(t *testing.T) {
	registry := GetRegistry()

	// Clear any existing sources
	for id := range registry.sources {
		_ = registry.RemoveSource(id)
	}

	const numOperations = 50
	var wg sync.WaitGroup
	wg.Add(numOperations * 2)

	// Half the goroutines create sources
	for i := 0; i < numOperations; i++ {
		go func(id int) {
			defer wg.Done()
			url := fmt.Sprintf("rtsp://concurrent-%d.local/stream", id)
			registry.MigrateSourceAtomic(url, SourceTypeRTSP)
		}(i)
	}

	// Other half try to remove sources
	for i := 0; i < numOperations; i++ {
		go func(id int) {
			defer wg.Done()
			// Small delay to let some sources be created first
			time.Sleep(time.Millisecond)
			url := fmt.Sprintf("rtsp://concurrent-%d.local/stream", id)
			_ = registry.RemoveSourceByConnection(url)
		}(i)
	}

	wg.Wait()

	// The registry should be in a consistent state
	// Some sources may remain (those created after removal attempts)
	for id, source := range registry.sources {
		// Verify connection map is consistent
		if mappedID, exists := registry.connectionMap[source.connectionString]; !exists {
			t.Errorf("Source %s exists but not in connectionMap", id)
		} else if mappedID != id {
			t.Errorf("Inconsistent mapping: source ID %s mapped to %s", id, mappedID)
		}
	}
}

// Removed custom string helpers - use strings.Contains from standard library instead