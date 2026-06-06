package diskmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FuzzPooledSliceOperations tests pooled slice operations with arbitrary sizes.
func FuzzPooledSliceOperations(f *testing.F) {
	// Seed with various sizes including edge cases
	f.Add(0)
	f.Add(1)
	f.Add(10)
	f.Add(100)
	f.Add(499) // Just under MaxPoolCapacity
	f.Add(500) // At MaxPoolCapacity
	f.Add(501) // Just over MaxPoolCapacity (triggers oversized path)
	f.Add(1000)
	f.Add(10000)

	f.Fuzz(func(t *testing.T, size int) {
		// Skip negative and excessively large sizes
		if size < 0 || size > 100000 {
			return
		}

		// Set up test config with known MaxPoolCapacity
		const maxPoolCap = 500
		cfg := &PoolConfig{
			InitialCapacity: 10,
			MaxPoolCapacity: maxPoolCap,
			MaxParseErrors:  100,
		}
		original := loadPoolConfig()
		poolConfig.Store(cfg)
		defer poolConfig.Store(original)

		// Track metrics before operation
		skipBefore := poolMetrics.SkipCount.Load()
		putBefore := poolMetrics.PutCount.Load()

		// Get a pooled slice
		slice := getPooledSlice()
		require.NotNil(t, slice, "getPooledSlice returned nil")

		// Create data of fuzzed size
		data := make([]FileInfo, size)
		for i := range data {
			data[i] = FileInfo{
				Path:       "/test/file.wav",
				Species:    "test_species",
				Confidence: 95,
			}
		}

		// SetData should not panic
		slice.SetData(data)

		// Data() should return the data with correct length
		got := slice.Data()
		require.NotNil(t, got, "Data() returned nil after SetData")
		assert.Len(t, *got, size, "Data() length mismatch")

		// Verify data integrity - spot check first and last elements
		if size > 0 {
			assert.Equal(t, "/test/file.wav", (*got)[0].Path, "first element Path mismatch")
			assert.Equal(t, 95, (*got)[size-1].Confidence, "last element Confidence mismatch")
		}

		// Release decides keep-vs-skip from the backing slice's capacity
		// (cap(*slice) > MaxPoolCapacity), not the data size. getPooledSlice()
		// draws from a process-wide sync.Pool that other tests populate with
		// larger-capacity slices, so the capacity after SetData can exceed
		// size. Capture the real capacity to assert against the same value
		// Release uses, which keeps the check deterministic regardless of
		// pool history.
		backingCap := cap(*got)

		// Release should not panic
		slice.Release()

		skipAfter := poolMetrics.SkipCount.Load()
		putAfter := poolMetrics.PutCount.Load()

		if backingCap > maxPoolCap {
			// Capacity over the limit: slice is skipped, not pooled.
			assert.Greater(t, skipAfter, skipBefore, "oversized backing (cap=%d) should increment SkipCount", backingCap)
		} else {
			// Capacity within the limit: slice is returned to the pool.
			assert.Greater(t, putAfter, putBefore, "normal backing (cap=%d) should increment PutCount", backingCap)
		}

		// Double release should be safe and not change metrics
		skipBeforeDouble := poolMetrics.SkipCount.Load()
		putBeforeDouble := poolMetrics.PutCount.Load()

		slice.Release()

		assert.Equal(t, skipBeforeDouble, poolMetrics.SkipCount.Load(), "double release should not change SkipCount")
		assert.Equal(t, putBeforeDouble, poolMetrics.PutCount.Load(), "double release should not change PutCount")
	})
}

// FuzzPooledSliceTakeOwnership tests TakeOwnership with arbitrary sizes.
func FuzzPooledSliceTakeOwnership(f *testing.F) {
	f.Add(0)
	f.Add(1)
	f.Add(50)
	f.Add(500)

	f.Fuzz(func(t *testing.T, size int) {
		if size < 0 || size > 10000 {
			return
		}

		slice := getPooledSlice()
		require.NotNil(t, slice, "getPooledSlice returned nil")

		data := make([]FileInfo, size)
		for i := range data {
			data[i] = FileInfo{
				Path:       "/test/file.wav",
				Species:    "species",
				Confidence: 90,
			}
		}
		slice.SetData(data)

		// TakeOwnership should not panic and should return correct size
		owned := slice.TakeOwnership()
		assert.Len(t, owned, size, "TakeOwnership() length mismatch")

		// After TakeOwnership, slice should be released
		assert.Nil(t, slice.slice, "slice.slice should be nil after TakeOwnership")
	})
}

// FuzzPoolConfig tests pool behavior with various config values.
func FuzzPoolConfig(f *testing.F) {
	f.Add(1, 10, 10)
	f.Add(10, 100, 50)
	f.Add(100, 1000, 100)
	f.Add(0, 0, 0)
	f.Add(1, 1, 1)

	f.Fuzz(func(t *testing.T, initial, maxPool, maxErrors int) {
		// Skip invalid configs
		if initial < 0 || maxPool < 0 || maxErrors < 0 {
			return
		}
		if initial > 10000 || maxPool > 10000 || maxErrors > 10000 {
			return
		}

		cfg := &PoolConfig{
			InitialCapacity: initial,
			MaxPoolCapacity: maxPool,
			MaxParseErrors:  maxErrors,
		}
		original := loadPoolConfig()
		poolConfig.Store(cfg)
		defer poolConfig.Store(original)

		// Basic operations should not panic
		slice := getPooledSlice()
		require.NotNil(t, slice, "getPooledSlice returned nil")

		slice.SetData([]FileInfo{{Path: "/test.wav"}})
		slice.Release()
	})
}
