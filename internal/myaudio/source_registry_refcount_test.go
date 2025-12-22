// source_registry_refcount_test.go - Tests for reference counting and source lifecycle management
package myaudio

import (
	"fmt"
	"strings"
	"sync"
	"testing"
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
	if err != nil {
		t.Fatalf("Failed to register source: %v", err)
	}

	// Initially no reference count
	if registry.refCounts[source.ID] != nil {
		t.Errorf("Expected nil reference count initially, got %v", *registry.refCounts[source.ID])
	}

	// Acquire first reference
	registry.AcquireSourceReference(source.ID)
	if registry.refCounts[source.ID] == nil || *registry.refCounts[source.ID] != 1 {
		t.Errorf("Expected reference count 1, got %v", registry.refCounts[source.ID])
	}

	// Acquire second reference
	registry.AcquireSourceReference(source.ID)
	if *registry.refCounts[source.ID] != 2 {
		t.Errorf("Expected reference count 2, got %v", *registry.refCounts[source.ID])
	}

	// Release one reference - source should still exist
	err = registry.ReleaseSourceReference(source.ID)
	if err != nil {
		t.Errorf("Failed to release reference: %v", err)
	}
	if *registry.refCounts[source.ID] != 1 {
		t.Errorf("Expected reference count 1 after release, got %v", *registry.refCounts[source.ID])
	}

	// Verify source still exists
	_, exists := registry.GetSourceByID(source.ID)
	if !exists {
		t.Error("Source should still exist with reference count 1")
	}

	// Release last reference - source should be removed
	err = registry.ReleaseSourceReference(source.ID)
	if err != nil {
		t.Errorf("Failed to release final reference: %v", err)
	}

	// Verify source is removed
	_, exists = registry.GetSourceByID(source.ID)
	if exists {
		t.Error("Source should be removed when reference count reaches 0")
	}

	// Verify reference count is cleaned up
	if _, hasRefCount := registry.refCounts[source.ID]; hasRefCount {
		t.Error("Reference count entry should be removed when source is removed")
	}
}

// TestReferenceCountingWithoutAcquire tests releasing without acquiring
func TestReferenceCountingWithoutAcquire(t *testing.T) {
	registry := createTestRegistry(t)

	// Register a source
	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	if err != nil {
		t.Fatalf("Failed to register source: %v", err)
	}

	// Release without acquire - should remove source immediately
	err = registry.ReleaseSourceReference(source.ID)
	if err != nil {
		t.Errorf("Failed to release reference: %v", err)
	}

	// Source should be removed
	_, exists := registry.GetSourceByID(source.ID)
	if exists {
		t.Error("Source should be removed when released without prior acquire")
	}
}

// TestReleaseNonExistentSource tests releasing a source that doesn't exist
func TestReleaseNonExistentSource(t *testing.T) {
	registry := createTestRegistry(t)

	// Try to release non-existent source
	err := registry.ReleaseSourceReference("non_existent_id")
	if err == nil {
		t.Error("Expected error when releasing non-existent source")
	}
	if err != nil && err.Error() != "source not found: non_existent_id" {
		t.Errorf("Unexpected error message: %v", err)
	}
}

// TestAcquireNonExistentSource tests acquiring reference for non-existent source
func TestAcquireNonExistentSource(t *testing.T) {
	registry := createTestRegistry(t)

	// Acquire reference for non-existent source - should be no-op
	registry.AcquireSourceReference("non_existent_id")

	// Verify no reference count was created
	if _, exists := registry.refCounts["non_existent_id"]; exists {
		t.Error("Reference count should not be created for non-existent source")
	}
}

// TestRemoveSourceIfUnused tests the atomic remove-if-unused operation
func TestRemoveSourceIfUnused(t *testing.T) {
	t.Run("source_in_use", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		if err != nil {
			t.Fatalf("Failed to register source: %v", err)
		}

		// Mock checker that reports source is in use
		inUseChecker := func(sourceID string) bool {
			return sourceID == source.ID // Source is in use
		}

		result, err := registry.RemoveSourceIfUnused(source.ID, inUseChecker)
		// When source is in use, expect both error and result code
		if err == nil {
			t.Error("Expected error when source is in use")
		}
		if result != RemoveSourceInUse {
			t.Errorf("Expected RemoveSourceInUse, got %v", result)
		}

		// Source should still exist
		_, exists := registry.GetSourceByID(source.ID)
		if !exists {
			t.Error("Source should not be removed when in use")
		}
	})

	t.Run("source_not_in_use", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		if err != nil {
			t.Fatalf("Failed to register source: %v", err)
		}

		// Mock checker that reports source is NOT in use
		notInUseChecker := func(sourceID string) bool {
			return false // Source is not in use
		}

		result, err := registry.RemoveSourceIfUnused(source.ID, notInUseChecker)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if result != RemoveSourceSuccess {
			t.Errorf("Expected RemoveSourceSuccess, got %v", result)
		}

		// Source should be removed
		_, exists := registry.GetSourceByID(source.ID)
		if exists {
			t.Error("Source should be removed when not in use")
		}
	})

	t.Run("multiple_checkers", func(t *testing.T) {
		registry := createTestRegistry(t)

		source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
			Type: SourceTypeRTSP,
		})
		if err != nil {
			t.Fatalf("Failed to register source: %v", err)
		}

		// First checker says not in use, second says in use
		checker1 := func(sourceID string) bool { return false }
		checker2 := func(sourceID string) bool { return true }

		result, err := registry.RemoveSourceIfUnused(source.ID, checker1, checker2)
		// When any checker says in use, expect error
		if err == nil {
			t.Error("Expected error when any checker reports in use")
		}
		if result != RemoveSourceInUse {
			t.Errorf("Expected RemoveSourceInUse when any checker reports in use, got %v", result)
		}

		// Source should still exist
		_, exists := registry.GetSourceByID(source.ID)
		if !exists {
			t.Error("Source should not be removed when any checker reports in use")
		}
	})

	t.Run("non_existent_source", func(t *testing.T) {
		registry := createTestRegistry(t)

		result, err := registry.RemoveSourceIfUnused("non_existent_id")
		// When source doesn't exist, expect error
		if err == nil {
			t.Error("Expected error when source doesn't exist")
		}
		if result != RemoveSourceNotFound {
			t.Errorf("Expected RemoveSourceNotFound, got %v", result)
		}
	})
}

// TestConcurrentReferenceOperations tests thread safety of reference counting
func TestConcurrentReferenceOperations(t *testing.T) {
	registry := createTestRegistry(t)

	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	if err != nil {
		t.Fatalf("Failed to register source: %v", err)
	}

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
		if err != nil {
			t.Fatalf("Failed to register source %d: %v", i, err)
		}
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
		if registry.refCounts[source.ID] == nil {
			t.Errorf("Source %d: expected reference count %d, got nil", i, expectedCount)
		} else if *registry.refCounts[source.ID] != expectedCount {
			t.Errorf("Source %d: expected reference count %d, got %d",
				i, expectedCount, *registry.refCounts[source.ID])
		}
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
			if exists {
				t.Errorf("Source %d should be removed after releasing all references", i)
			}
		} else {
			if !exists {
				t.Errorf("Source %d should still exist with references", i)
			}
		}
	}
}

// TestRemoveSourceWithReferences tests that RemoveSource bypasses reference counting
func TestRemoveSourceWithReferences(t *testing.T) {
	registry := createTestRegistry(t)

	source, err := registry.RegisterSource("rtsp://test.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	if err != nil {
		t.Fatalf("Failed to register source: %v", err)
	}

	// Acquire multiple references
	registry.AcquireSourceReference(source.ID)
	registry.AcquireSourceReference(source.ID)
	registry.AcquireSourceReference(source.ID)

	// Force remove should work even with references
	err = registry.RemoveSource(source.ID)
	if err != nil {
		t.Errorf("Failed to remove source: %v", err)
	}

	// Source should be gone
	_, exists := registry.GetSourceByID(source.ID)
	if exists {
		t.Error("Source should be removed by RemoveSource even with references")
	}

	// Reference count should be cleaned up
	if _, hasRefCount := registry.refCounts[source.ID]; hasRefCount {
		t.Error("Reference count should be cleaned up after RemoveSource")
	}
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
		if err != nil {
			t.Fatalf("Failed to register source: %v", err)
		}

		// Acquire and release references
		registry.AcquireSourceReference(source.ID)
		registry.AcquireSourceReference(source.ID)
		_ = registry.ReleaseSourceReference(source.ID)
		_ = registry.ReleaseSourceReference(source.ID)
	}

	// All sources should be removed
	if len(registry.sources) != 0 {
		t.Errorf("Expected 0 sources after cleanup, got %d", len(registry.sources))
	}

	// All reference counts should be cleaned up
	if len(registry.refCounts) != 0 {
		t.Errorf("Expected 0 reference counts after cleanup, got %d", len(registry.refCounts))
	}

	// Connection map should be empty
	if len(registry.connectionMap) != 0 {
		t.Errorf("Expected 0 connection mappings after cleanup, got %d", len(registry.connectionMap))
	}
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
		if err != nil {
			t.Fatalf("Failed to register %s: %v", conn, err)
		}
		sourceIDs[conn] = source.ID

		// Simulate buffer allocation (acquire reference)
		registry.AcquireSourceReference(source.ID)
	}

	// Verify all sources exist
	if len(registry.sources) != 3 {
		t.Errorf("Expected 3 sources, got %d", len(registry.sources))
	}

	// Phase 2: User removes one camera, adds another
	// Remove cam1
	cam1ID := sourceIDs["rtsp://cam1.local/stream"]
	err := registry.ReleaseSourceReference(cam1ID)
	if err != nil {
		t.Errorf("Failed to release cam1: %v", err)
	}

	// Add cam3
	source, err := registry.RegisterSource("rtsp://cam3.local/stream", SourceConfig{
		Type: SourceTypeRTSP,
	})
	if err != nil {
		t.Fatalf("Failed to register cam3: %v", err)
	}
	sourceIDs["rtsp://cam3.local/stream"] = source.ID
	registry.AcquireSourceReference(source.ID)

	// Should still have 3 sources (cam1 removed, cam3 added)
	if len(registry.sources) != 3 {
		t.Errorf("After swap, expected 3 sources, got %d", len(registry.sources))
	}

	// Verify cam1 is gone
	if _, exists := registry.GetSourceByID(cam1ID); exists {
		t.Error("cam1 should be removed")
	}

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
	if result != RemoveSourceInUse {
		t.Errorf("Expected RemoveSourceInUse for cam2, got %v", result)
	}

	// Verify cam2 still exists
	if _, exists := registry.GetSourceByID(cam2ID); !exists {
		t.Error("cam2 should still exist when buffer reports in use")
	}

	// Phase 4: Clean shutdown - release all references
	for _, id := range sourceIDs {
		_ = registry.ReleaseSourceReference(id) // Some might already be released
	}

	// Verify cleanup
	remainingSources := len(registry.sources)
	if remainingSources > 0 {
		t.Errorf("Expected 0 sources after cleanup, got %d", remainingSources)
		// Log what's left
		for id, source := range registry.sources {
			connStr, _ := source.GetConnectionString()
			t.Logf("Remaining source: %s -> %s", id, connStr)
		}
	}
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
		for i := range b.N {
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
		}
	})

	b.Run("Release", func(b *testing.B) {
		b.ReportAllocs()
		// Pre-acquire references
		for i := 0; i < b.N; i++ {
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
		}

		b.ResetTimer()
		for i := range b.N {
			source := sources[i%10]
			_ = registry.ReleaseSourceReference(source.ID)
		}
	})

	b.Run("AcquireRelease", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			source := sources[i%10]
			registry.AcquireSourceReference(source.ID)
			_ = registry.ReleaseSourceReference(source.ID)
		}
	})
}
