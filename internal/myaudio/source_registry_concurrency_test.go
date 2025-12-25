// source_registry_concurrency_test.go - Tests for concurrent operations and security validation
package myaudio

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRaceConditionFix verifies that GetOrCreateSource prevents race conditions
func TestRaceConditionFix(t *testing.T) {
	t.Attr("component", "source-registry")
	t.Attr("test-type", "concurrency")

	registry := newTestRegistry()

	// Test concurrent migrations of the same source
	const numGoroutines = 100
	const testURL = "rtsp://race-test.local/stream"

	results := make(chan string, numGoroutines)
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
			var id string
			if source != nil {
				id = source.ID
			}
			results <- id
		})
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
		for id, count := range ids {
			t.Logf("ID %s returned %d times", id, count)
		}
	}
	assert.Len(t, ids, 1, "Race condition detected: all goroutines should return same ID")

	// Verify only one source was created
	registry.mu.RLock()
	sourcesCount := len(registry.sources)
	registry.mu.RUnlock()
	assert.Equal(t, 1, sourcesCount, "Expected 1 source in registry")
}

// TestMemoryLeakFix verifies that sources can be properly cleaned up
func TestMemoryLeakFix(t *testing.T) {
	t.Attr("component", "source-registry")
	t.Attr("test-type", "cleanup")
	registry := newTestRegistry()

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
		require.NoError(t, err, "Failed to register source")
		sourceIDs = append(sourceIDs, source.ID)
	}

	// Verify sources are registered
	registry.mu.RLock()
	sourcesCount := len(registry.sources)
	registry.mu.RUnlock()
	assert.Equal(t, 3, sourcesCount, "Expected 3 sources")

	// Remove sources
	for _, id := range sourceIDs {
		err := registry.RemoveSource(id)
		require.NoError(t, err, "Failed to remove source %s", id)
	}

	// Verify sources are removed
	registry.mu.RLock()
	sourcesCount = len(registry.sources)
	connectionMapCount := len(registry.connectionMap)
	registry.mu.RUnlock()
	assert.Equal(t, 0, sourcesCount, "Expected 0 sources after cleanup")
	assert.Equal(t, 0, connectionMapCount, "Expected 0 entries in connectionMap after cleanup")
}

// TestInactiveSourceCleanup verifies that inactive sources can be cleaned up
func TestInactiveSourceCleanup(t *testing.T) {
	t.Attr("component", "source-registry")
	t.Attr("test-type", "cleanup")
	registry := newTestRegistry()

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
	assert.Equal(t, 1, removed, "Expected to remove 1 source")

	// Verify correct sources remain
	registry.mu.RLock()
	_, activeExists := registry.sources["active_001"]
	_, recentExists := registry.sources["recent_001"]
	_, oldExists := registry.sources["old_001"]
	registry.mu.RUnlock()

	assert.True(t, activeExists, "Active source should not be removed")
	assert.True(t, recentExists, "Recent inactive source should not be removed")
	assert.False(t, oldExists, "Old inactive source should be removed")
}

// TestURLValidation verifies that dangerous URLs are rejected
func TestURLValidation(t *testing.T) {
	t.Attr("component", "source-registry")
	t.Attr("test-type", "validation")
	registry := GetRegistry()

	testCases := []struct {
		name          string
		url           string
		sourceType    SourceType
		shouldFail    bool
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
			name:          "Shell variable injection",
			url:           "rtsp://test.com/stream$(whoami)",
			sourceType:    SourceTypeRTSP,
			shouldFail:    true,
			errorContains: "dangerous pattern",
		},
		{
			name:          "Pipe injection",
			url:           "test.wav | cat /etc/passwd",
			sourceType:    SourceTypeFile,
			shouldFail:    true,
			errorContains: "dangerous pattern",
		},
		{
			name:       "Valid audio device",
			url:        "hw:1,0",
			sourceType: SourceTypeAudioCard,
			shouldFail: false,
		},
		{
			name:          "Invalid audio device",
			url:           "/dev/null",
			sourceType:    SourceTypeAudioCard,
			shouldFail:    true,
			errorContains: "invalid audio device",
		},
		{
			name:          "Directory traversal in file",
			url:           "../../../etc/passwd",
			sourceType:    SourceTypeFile,
			shouldFail:    true,
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
				require.Error(t, err, "Expected validation to fail for %s", tc.url)
				if tc.errorContains != "" {
					assert.Contains(t, err.Error(), tc.errorContains, "Expected error to contain '%s'", tc.errorContains)
				}
			} else {
				require.NoError(t, err, "Expected validation to pass for %s", tc.url)
			}
		})
	}
}

// TestConcurrentMigrationAndCleanup tests that migration and cleanup don't race
func TestConcurrentMigrationAndCleanup(t *testing.T) {
	t.Attr("component", "source-registry")
	t.Attr("test-type", "concurrency")

	synctest.Test(t, func(t *testing.T) {
		registry := GetRegistry()

		// Clear any existing sources
		for id := range registry.sources {
			_ = registry.RemoveSource(id)
		}

		const numOperations = 50
		var wg sync.WaitGroup

		// Create start barrier for coordinating creators and removers
		startCh := make(chan struct{})
		var creatorsStarted sync.WaitGroup

		// Half the goroutines create sources
		for i := range numOperations {
			id := i
			creatorsStarted.Add(1)
			wg.Go(func() {
				defer creatorsStarted.Done() // Signal that this creator has started
				url := fmt.Sprintf("rtsp://concurrent-%d.local/stream", id)
				registry.GetOrCreateSource(url, SourceTypeRTSP)
			})
		}

		// Wait for all creators to start, then release removers
		wg.Go(func() {
			creatorsStarted.Wait()
			close(startCh) // Release all removers
		})

		// Other half try to remove sources - wait for start signal
		for i := range numOperations {
			id := i
			wg.Go(func() {
				<-startCh // Wait for creators to start before proceeding
				url := fmt.Sprintf("rtsp://concurrent-%d.local/stream", id)
				_ = registry.RemoveSourceByConnection(url)
			})
		}

		wg.Wait()
	})

	// The registry should be in a consistent state
	// Some sources may remain (those created after removal attempts)
	// Use safe accessors instead of direct map iteration to avoid race conditions
	sources := registry.ListSources()
	for _, source := range sources {
		// Get the connection string safely and verify mapping consistency
		connStr, err := source.GetConnectionString()
		if err != nil {
			t.Logf("Failed to get connection string for source %s: %v", source.ID, err)
			continue
		}

		// Verify the source can be found by its connection string
		foundSource, exists := registry.GetSourceByConnection(connStr)
		if assert.True(t, exists, "Source %s exists but not found by connection string %s", source.ID, source.SafeString) {
			assert.Equal(t, source.ID, foundSource.ID, "Inconsistent mapping: source ID should match")
		}
	}
}

// Removed custom string helpers - use strings.Contains from standard library instead
