package diskmanager

import (
	"runtime"
	"testing"
	"time"
)

// TestPooledSliceReleaseMemoryLeak verifies that oversized slices properly
// release their memory references when not pooled
func TestPooledSliceReleaseMemoryLeak(t *testing.T) {
	// Save original config and restore after test
	originalConfig := loadPoolConfig()
	defer func() {
		poolConfig.Store(originalConfig)
	}()

	// Set a small max pool capacity to trigger the oversized path
	testConfig := &PoolConfig{
		InitialCapacity: 10,
		MaxPoolCapacity: 100,
		MaxParseErrors:  100,
	}
	poolConfig.Store(testConfig)

	// Create a large slice that exceeds MaxPoolCapacity
	largeSlice := getPooledSlice()

	// Resize it to be larger than MaxPoolCapacity
	largeData := make([]FileInfo, 200)
	for i := range largeData {
		largeData[i] = FileInfo{
			Path:       "/test/file.wav",
			Species:    "test_species",
			Confidence: 95,
			Timestamp:  time.Now(),
		}
	}
	largeSlice.SetData(largeData)

	// Verify the slice has data
	if len(*largeSlice.Data()) != 200 {
		t.Errorf("Expected slice length 200, got %d", len(*largeSlice.Data()))
	}

	// Store a reference to check if it gets cleared
	slicePtr := largeSlice.Data()

	// Release the slice (should trigger the oversized path)
	largeSlice.Release()

	// Verify that the slice reference is cleared
	if largeSlice.slice != nil {
		t.Error("Expected slice reference to be nil after release of oversized slice")
	}

	// The original slice should have been cleared
	if len(*slicePtr) != 0 {
		t.Error("Expected original slice to be cleared (length 0)")
	}

	// Force garbage collection to ensure memory can be freed
	runtime.GC()
	runtime.GC()

	// Verify metrics show the skip
	if poolMetrics.SkipCount.Load() == 0 {
		t.Error("Expected SkipCount to be incremented for oversized slice")
	}
}

// TestPooledSliceNormalPooling verifies normal pooling behavior still works
func TestPooledSliceNormalPooling(t *testing.T) {
	// Save original config and restore after test
	originalConfig := loadPoolConfig()
	defer func() {
		poolConfig.Store(originalConfig)
	}()

	// Set config with reasonable pool capacity
	testConfig := &PoolConfig{
		InitialCapacity: 10,
		MaxPoolCapacity: 1000,
		MaxParseErrors:  100,
	}
	poolConfig.Store(testConfig)

	// Reset metrics for clean test
	poolMetrics.PutCount.Store(0)
	poolMetrics.SkipCount.Store(0)

	// Get a pooled slice
	slice1 := getPooledSlice()

	// Add some data
	testData := []FileInfo{
		{Path: "/test1.wav", Species: "species1", Confidence: 90},
		{Path: "/test2.wav", Species: "species2", Confidence: 85},
	}
	slice1.SetData(testData)

	// Release it (should go back to pool)
	slice1.Release()

	// Verify it was pooled, not skipped
	if poolMetrics.PutCount.Load() == 0 {
		t.Error("Expected PutCount to be incremented for normal-sized slice")
	}
	if poolMetrics.SkipCount.Load() != 0 {
		t.Error("Expected SkipCount to remain 0 for normal-sized slice")
	}

	// Get another slice - should reuse from pool
	slice2 := getPooledSlice()

	// Verify it's been reset
	if len(*slice2.Data()) != 0 {
		t.Error("Expected reused slice to have length 0")
	}

	slice2.Release()
}

// TestPooledSliceDoubleRelease verifies double release is safe
func TestPooledSliceDoubleRelease(t *testing.T) {
	slice := getPooledSlice()

	// First release
	slice.Release()

	// Second release should be safe (no panic)
	slice.Release()

	// Verify slice is still nil
	if slice.slice != nil {
		t.Error("Expected slice to remain nil after double release")
	}
}

// TestPooledSliceTakeOwnership verifies ownership transfer
func TestPooledSliceTakeOwnership(t *testing.T) {
	slice := getPooledSlice()

	testData := []FileInfo{
		{Path: "/test.wav", Species: "test", Confidence: 95},
	}
	slice.SetData(testData)

	// Take ownership
	owned := slice.TakeOwnership()

	// Verify data was copied
	if len(owned) != 1 {
		t.Errorf("Expected owned slice length 1, got %d", len(owned))
	}
	if owned[0].Path != "/test.wav" {
		t.Errorf("Expected path /test.wav, got %s", owned[0].Path)
	}

	// Original should be released
	if slice.slice != nil {
		t.Error("Expected original slice to be nil after ownership transfer")
	}
}

// BenchmarkPooledSliceRelease benchmarks the release operation
func BenchmarkPooledSliceRelease(b *testing.B) {
	// Test both normal and oversized paths
	configs := []struct {
		name string
		size int
	}{
		{"Normal", 50},
		{"Oversized", 500},
	}

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			// Set up test config
			testConfig := &PoolConfig{
				InitialCapacity: 10,
				MaxPoolCapacity: 100,
				MaxParseErrors:  100,
			}
			poolConfig.Store(testConfig)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				slice := getPooledSlice()
				data := make([]FileInfo, cfg.size)
				slice.SetData(data)
				slice.Release()
			}
		})
	}
}
