// source_registry_refcount_test.go - Tests for reference counting and source lifecycle management
package myaudio

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create an isolated test registry
func createTestRegistry(tb testing.TB) *AudioSourceRegistry {
	tb.Helper()
	return &AudioSourceRegistry{
		sources:       make(map[string]*AudioSource),
		connectionMap: make(map[string]string),
		refCounts:     make(map[string]*int32),
		logger:        getTestLogger(),
	}
}

// TestReferenceCountingBasic tests basic acquire and release operations
func TestReferenceCountingBasic(t *testing.T) {
	registry := createTestRegistry(t)

	// Register a source
	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	require.NoError(t, err, "Failed to register source")

	// Initially no reference count
	assert.Nil(t, registry.refCounts[source.ID], "Expected nil reference count initially")

	// Acquire first reference
	registry.AcquireSourceReference(source.ID)
	require.NotNil(t, registry.refCounts[source.ID])
	assert.Equal(t, int32(1), *registry.refCounts[source.ID], "Expected reference count 1")

	// Acquire second reference
	registry.AcquireSourceReference(source.ID)
	assert.Equal(t, int32(2), *registry.refCounts[source.ID], "Expected reference count 2")

	// Release one reference - source should still exist
	err = registry.ReleaseSourceReference(source.ID)
	require.NoError(t, err, "Failed to release reference")
	assert.Equal(t, int32(1), *registry.refCounts[source.ID], "Expected reference count 1 after release")

	// Verify source still exists
	_, exists := registry.GetSourceByID(source.ID)
	assert.True(t, exists, "Source should still exist with reference count 1")

	// Release last reference - source should be removed
	err = registry.ReleaseSourceReference(source.ID)
	require.NoError(t, err, "Failed to release final reference")

	// Verify source is removed
	_, exists = registry.GetSourceByID(source.ID)
	assert.False(t, exists, "Source should be removed when reference count reaches 0")

	// Verify reference count is cleaned up
	_, hasRefCount := registry.refCounts[source.ID]
	assert.False(t, hasRefCount, "Reference count entry should be removed when source is removed")
}

// TestReferenceCountingWithoutAcquire tests releasing without acquiring
func TestReferenceCountingWithoutAcquire(t *testing.T) {
	registry := createTestRegistry(t)

	// Register a source
	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	require.NoError(t, err, "Failed to register source")

	// Release without acquire - should remove source immediately
	err = registry.ReleaseSourceReference(source.ID)
	require.NoError(t, err, "Failed to release reference")

	// Source should be removed
	_, exists := registry.GetSourceByID(source.ID)
	assert.False(t, exists, "Source should be removed when released without prior acquire")
}

// TestReleaseNonExistentSource tests releasing a source that doesn't exist
func TestReleaseNonExistentSource(t *testing.T) {
	registry := createTestRegistry(t)

	// Try to release non-existent source
	err := registry.ReleaseSourceReference("non_existent_id")
	require.Error(t, err, "Expected error when releasing non-existent source")
	assert.Equal(t, "source not found: non_existent_id", err.Error(), "Unexpected error message")
}

// TestAcquireNonExistentSource tests acquiring reference for non-existent source
func TestAcquireNonExistentSource(t *testing.T) {
	registry := createTestRegistry(t)

	// Acquire reference for non-existent source - should be no-op
	registry.AcquireSourceReference("non_existent_id")

	// Verify no reference count was created
	_, exists := registry.refCounts["non_existent_id"]
	assert.False(t, exists, "Reference count should not be created for non-existent source")
}

// TestRemoveSourceIfUnused tests the atomic remove-if-unused operation
func TestRemoveSourceIfUnused(t *testing.T) {
	t.Run("source_in_use", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		require.NoError(t, err, "Failed to register source")

		// Mock checker that reports source is in use
		inUseChecker := func(sourceID string) bool {
			return sourceID == source.ID // Source is in use
		}

		result, err := registry.RemoveSourceIfUnused(source.ID, inUseChecker)
		// When source is in use, expect both error and result code
		require.Error(t, err, "Expected error when source is in use")
		assert.Equal(t, RemoveSourceInUse, result, "Expected RemoveSourceInUse")

		// Source should still exist
		_, exists := registry.GetSourceByID(source.ID)
		assert.True(t, exists, "Source should not be removed when in use")
	})

	t.Run("source_not_in_use", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		require.NoError(t, err, "Failed to register source")

		// Mock checker that reports source is NOT in use
		notInUseChecker := func(sourceID string) bool {
			return false // Source is not in use
		}

		result, err := registry.RemoveSourceIfUnused(source.ID, notInUseChecker)
		require.NoError(t, err, "Unexpected error")
		assert.Equal(t, RemoveSourceSuccess, result, "Expected RemoveSourceSuccess")

		// Source should be removed
		_, exists := registry.GetSourceByID(source.ID)
		assert.False(t, exists, "Source should be removed when not in use")
	})

	t.Run("multiple_checkers", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		require.NoError(t, err, "Failed to register source")

		// First checker says not in use, second says in use
		checker1 := func(sourceID string) bool { return false }
		checker2 := func(sourceID string) bool { return true }

		result, err := registry.RemoveSourceIfUnused(source.ID, checker1, checker2)
		// When any checker says in use, expect error
		require.Error(t, err, "Expected error when any checker reports in use")
		assert.Equal(t, RemoveSourceInUse, result, "Expected RemoveSourceInUse when any checker reports in use")

		// Source should still exist
		_, exists := registry.GetSourceByID(source.ID)
		assert.True(t, exists, "Source should not be removed when any checker reports in use")
	})

	t.Run("non_existent_source", func(t *testing.T) {
		registry := createTestRegistry(t)

		result, err := registry.RemoveSourceIfUnused("non_existent_id")
		// When source doesn't exist, expect error
		require.Error(t, err, "Expected error when source doesn't exist")
		assert.Equal(t, RemoveSourceNotFound, result, "Expected RemoveSourceNotFound")
	})
}

// TestConcurrentReferenceOperations tests thread safety of reference counting
func TestConcurrentReferenceOperations(t *testing.T) {
	registry := createTestRegistry(t)

	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	require.NoError(t, err, "Failed to register source")

	const numGoroutines = 100
	var wg sync.WaitGroup
	startBarrier := make(chan struct{})

	// First, acquire references to ensure there's something to release
	for range numGoroutines / 2 {
		registry.AcquireSourceReference(source.ID)
	}

	// Launch concurrent operations
	for i := range numGoroutines {
		wg.Add(1)
		if i%2 == 0 {
			// Acquire reference
			go func() {
				defer wg.Done()
				<-startBarrier // Wait for signal to start
				registry.AcquireSourceReference(source.ID)
			}()
		} else {
			// Release reference
			go func() {
				defer wg.Done()
				<-startBarrier                                 // Wait for signal to start
				_ = registry.ReleaseSourceReference(source.ID) // Ignore error
			}()
		}
	}

	// Signal all goroutines to start simultaneously
	close(startBarrier)

	wg.Wait()

	// The exact reference count is unpredictable due to timing,
	// but the operations should complete without panic/race

	// Clean up by releasing any remaining references
	for {
		err := registry.ReleaseSourceReference(source.ID)
		if err != nil {
			break // Source removed or doesn't exist
		}
	}
}

// TestReferenceCountingWithMultipleSources tests reference counting across multiple sources
func TestReferenceCountingWithMultipleSources(t *testing.T) {
	registry := createTestRegistry(t)

	// Register multiple sources
	sources := make([]*AudioSource, 0, 5)
	for i := range 5 {
		source, err := registry.RegisterSource(
			fmt.Sprintf("rtsp://cam%d.local/stream", i),
			SourceConfig{Type: SourceTypeRTSP},
		)
		require.NoError(t, err, "Failed to register source %d", i)
		sources = append(sources, source)
	}

	// Acquire different numbers of references for each source
	for i, source := range sources {
		for j := 0; j <= i; j++ {
			registry.AcquireSourceReference(source.ID)
		}
	}

	// Verify reference counts
	for i, source := range sources {
		expectedCount := int32(i + 1) //nolint:gosec // G115: i is a small loop index (0-4)
		require.NotNil(t, registry.refCounts[source.ID], "Source %d: expected reference count %d, got nil", i, expectedCount)
		assert.Equal(t, expectedCount, *registry.refCounts[source.ID], "Source %d: expected reference count %d", i, expectedCount)
	}

	// Release all references for even-indexed sources
	for i, source := range sources {
		if i%2 == 0 {
			for j := 0; j <= i; j++ {
				_ = registry.ReleaseSourceReference(source.ID)
			}
		}
	}

	// Verify even sources are removed, odd sources remain
	for i, source := range sources {
		_, exists := registry.GetSourceByID(source.ID)
		if i%2 == 0 {
			assert.False(t, exists, "Source %d should be removed after releasing all references", i)
		} else {
			assert.True(t, exists, "Source %d should still exist with references", i)
		}
	}
}

// TestRemoveSourceWithReferences tests that RemoveSource bypasses reference counting
func TestRemoveSourceWithReferences(t *testing.T) {
	registry := createTestRegistry(t)

	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	require.NoError(t, err, "Failed to register source")

	// Acquire multiple references
	registry.AcquireSourceReference(source.ID)
	registry.AcquireSourceReference(source.ID)
	registry.AcquireSourceReference(source.ID)

	// Force remove should work even with references
	err = registry.RemoveSource(source.ID)
	require.NoError(t, err, "Failed to remove source")

	// Source should be gone
	_, exists := registry.GetSourceByID(source.ID)
	assert.False(t, exists, "Source should be removed by RemoveSource even with references")

	// Reference count should be cleaned up
	_, hasRefCount := registry.refCounts[source.ID]
	assert.False(t, hasRefCount, "Reference count should be cleaned up after RemoveSource")
}

// TestReferenceCountingMemoryLeak tests that reference counts don't cause memory leaks
func TestReferenceCountingMemoryLeak(t *testing.T) {
	registry := createTestRegistry(t)

	// Create and remove many sources with references
	for i := range 100 {
		source, err := registry.RegisterSource(
			fmt.Sprintf("rtsp://test%d.local/stream", i),
			SourceConfig{Type: SourceTypeRTSP},
		)
		require.NoError(t, err, "Failed to register source")

		// Acquire and release references
		registry.AcquireSourceReference(source.ID)
		registry.AcquireSourceReference(source.ID)
		_ = registry.ReleaseSourceReference(source.ID)
		_ = registry.ReleaseSourceReference(source.ID)
	}

	// All sources should be removed
	assert.Empty(t, registry.sources, "Expected 0 sources after cleanup")

	// All reference counts should be cleaned up
	assert.Empty(t, registry.refCounts, "Expected 0 reference counts after cleanup")

	// Connection map should be empty
	assert.Empty(t, registry.connectionMap, "Expected 0 connection mappings after cleanup")
}

// TestDynamicSourceLifecycle simulates real-world source add/remove operations
func TestDynamicSourceLifecycle(t *testing.T) {
	registry := createTestRegistry(t)

	// Simulate a system that dynamically adds and removes sources
	// like when users change configuration

	// Phase 1: Add initial sources
	initialSources := []string{
		"rtsp://cam1.local/stream",
		"rtsp://cam2.local/stream",
		"hw:1,0", // Audio device
	}

	sourceIDs := make(map[string]string)
	for _, conn := range initialSources {
		sourceType := SourceTypeRTSP
		if strings.HasPrefix(conn, "hw:") {
			sourceType = SourceTypeAudioCard
		}

		source, err := registry.RegisterSource(conn, SourceConfig{
			Type: sourceType,
		})
		require.NoError(t, err, "Failed to register %s", conn)
		sourceIDs[conn] = source.ID

		// Simulate buffer allocation (acquire reference)
		registry.AcquireSourceReference(source.ID)
	}

	// Verify all sources exist
	assert.Len(t, registry.sources, 3, "Expected 3 sources")

	// Phase 2: User removes one camera, adds another
	// Remove cam1
	cam1ID := sourceIDs["rtsp://cam1.local/stream"]
	err := registry.ReleaseSourceReference(cam1ID)
	require.NoError(t, err, "Failed to release cam1")

	// Add cam3
	source, err := registry.RegisterSource("rtsp://cam3.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	require.NoError(t, err, "Failed to register cam3")
	sourceIDs["rtsp://cam3.local/stream"] = source.ID
	registry.AcquireSourceReference(source.ID)

	// Should still have 3 sources (cam1 removed, cam3 added)
	assert.Len(t, registry.sources, 3, "After swap, expected 3 sources")

	// Verify cam1 is gone
	_, exists := registry.GetSourceByID(cam1ID)
	assert.False(t, exists, "cam1 should be removed")

	// Phase 3: Simulate source failure and recovery
	// Cam2 fails but we keep the reference (might reconnect)
	cam2ID := sourceIDs["rtsp://cam2.local/stream"]
	cam2Source, _ := registry.GetSourceByID(cam2ID)
	if cam2Source != nil {
		cam2Source.IsActive = false
		cam2Source.ErrorCount++
	}

	// RemoveSourceIfUnused only checks the provided checkers, not internal ref counts
	// So we need a checker that simulates buffer still using it
	mockChecker := func(id string) bool {
		return id == cam2ID // Simulate buffer still using cam2
	}
	result, _ := registry.RemoveSourceIfUnused(cam2ID, mockChecker)
	assert.Equal(t, RemoveSourceInUse, result, "Expected RemoveSourceInUse for cam2")

	// Verify cam2 still exists
	_, exists = registry.GetSourceByID(cam2ID)
	assert.True(t, exists, "cam2 should still exist when buffer reports in use")

	// Phase 4: Clean shutdown - release all references
	for _, id := range sourceIDs {
		_ = registry.ReleaseSourceReference(id) // Some might already be released
	}

	// Verify cleanup
	remainingSources := len(registry.sources)
	if remainingSources > 0 {
		// Log what's left
		for id, source := range registry.sources {
			connStr, _ := source.GetConnectionString()
			t.Logf("Remaining source: %s -> %s", id, connStr)
		}
	}
	assert.Equal(t, 0, remainingSources, "Expected 0 sources after cleanup")
}

// BenchmarkReferenceOperations benchmarks reference counting operations
func BenchmarkReferenceOperations(b *testing.B) {
	registry := createTestRegistry(b)

	// Pre-create sources
	sources := make([]*AudioSource, 10)
	for i := range 10 {
		source, _ := registry.RegisterSource(
			fmt.Sprintf("rtsp://bench%d.local/stream", i),
			SourceConfig{Type: SourceTypeRTSP},
		)
		sources[i] = source
	}

	b.ResetTimer()

	b.Run("Acquire", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
		}
	})

	b.Run("Release", func(b *testing.B) {
		b.ReportAllocs()
		// Pre-acquire references
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
		}

		b.ResetTimer()
		i := 0
		for b.Loop() {
			source := sources[i%10]
			_ = registry.ReleaseSourceReference(source.ID)
			i++
		}
	})

	b.Run("AcquireRelease", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
			_ = registry.ReleaseSourceReference(source.ID)
			i++
		}
	})
}
