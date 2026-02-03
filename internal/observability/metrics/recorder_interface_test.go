package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordOperation verifies RecordOperation functionality of TestRecorder.
func TestRecordOperation(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordOperation("prediction", "success")
	recorder.RecordOperation("prediction", "success")
	recorder.RecordOperation("prediction", "error")
	recorder.RecordOperation("model_load", "success")

	assert.Equal(t, 2, recorder.GetOperationCount("prediction", "success"), "should have 2 successful predictions")
	assert.Equal(t, 1, recorder.GetOperationCount("prediction", "error"), "should have 1 error prediction")
	assert.Equal(t, 1, recorder.GetOperationCount("model_load", "success"), "should have 1 successful model load")
	assert.Equal(t, 0, recorder.GetOperationCount("model_load", "error"), "should have 0 error model loads")
}

// TestRecordDuration verifies RecordDuration functionality of TestRecorder.
func TestRecordDuration(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordDuration("prediction", 0.123)
	recorder.RecordDuration("prediction", 0.456)
	recorder.RecordDuration("chunk_process", 0.789)

	predDurations := recorder.GetDurations("prediction")
	require.Len(t, predDurations, 2, "should have 2 prediction durations")
	assert.InDelta(t, 0.123, predDurations[0], 0.01, "first prediction duration should be 0.123")
	assert.InDelta(t, 0.456, predDurations[1], 0.01, "second prediction duration should be 0.456")

	chunkDurations := recorder.GetDurations("chunk_process")
	require.Len(t, chunkDurations, 1, "should have 1 chunk process duration")
	assert.InDelta(t, 0.789, chunkDurations[0], 0.01, "chunk duration should be 0.789")

	// Test non-existent operation
	durations := recorder.GetDurations("non_existent")
	assert.Nil(t, durations, "should return nil for non-existent operation")
}

// TestRecordError verifies RecordError functionality of TestRecorder.
func TestRecordError(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordError("prediction", "validation")
	recorder.RecordError("prediction", "validation")
	recorder.RecordError("prediction", "model_error")
	recorder.RecordError("db_query", "connection")

	assert.Equal(t, 2, recorder.GetErrorCount("prediction", "validation"), "should have 2 validation errors")
	assert.Equal(t, 1, recorder.GetErrorCount("prediction", "model_error"), "should have 1 model error")
	assert.Equal(t, 1, recorder.GetErrorCount("db_query", "connection"), "should have 1 connection error")
	assert.Equal(t, 0, recorder.GetErrorCount("db_query", "timeout"), "should have 0 timeout errors")
}

// TestRecorderThreadSafety verifies thread safety of TestRecorder.
func TestRecorderThreadSafety(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	done := make(chan bool)
	numGoroutines := 10
	opsPerGoroutine := 100

	for range numGoroutines {
		go func() {
			for range opsPerGoroutine {
				recorder.RecordOperation("concurrent", "success")
				recorder.RecordDuration("concurrent", 0.001)
				recorder.RecordError("concurrent", "test")
			}
			done <- true
		}()
	}

	for range numGoroutines {
		<-done
	}

	expectedCount := numGoroutines * opsPerGoroutine
	assert.Equal(t, expectedCount, recorder.GetOperationCount("concurrent", "success"),
		"should have correct operation count after concurrent access")

	durations := recorder.GetDurations("concurrent")
	assert.Len(t, durations, expectedCount, "should have correct duration count after concurrent access")

	assert.Equal(t, expectedCount, recorder.GetErrorCount("concurrent", "test"),
		"should have correct error count after concurrent access")
}

// TestGetAllOperations verifies GetAllOperations functionality of TestRecorder.
func TestGetAllOperations(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordOperation("op1", "success")
	recorder.RecordOperation("op1", "error")
	recorder.RecordOperation("op2", "success")

	all := recorder.GetAllOperations()
	assert.Len(t, all, 2, "should have 2 different operations")

	assert.Equal(t, 1, all["op1"]["success"], "op1 should have 1 success")
	assert.Equal(t, 1, all["op1"]["error"], "op1 should have 1 error")
	assert.Equal(t, 1, all["op2"]["success"], "op2 should have 1 success")
}

// TestGetAllErrors verifies GetAllErrors functionality of TestRecorder.
func TestGetAllErrors(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordError("op1", "type1")
	recorder.RecordError("op1", "type2")
	recorder.RecordError("op2", "type1")

	all := recorder.GetAllErrors()
	assert.Len(t, all, 2, "should have 2 operations with errors")

	assert.Equal(t, 1, all["op1"]["type1"], "op1 should have 1 type1 error")
	assert.Equal(t, 1, all["op1"]["type2"], "op1 should have 1 type2 error")
	assert.Equal(t, 1, all["op2"]["type1"], "op2 should have 1 type1 error")
}

// TestHasRecordedMetrics verifies HasRecordedMetrics functionality of TestRecorder.
func TestHasRecordedMetrics(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()

	// Initially should have no metrics
	assert.False(t, recorder.HasRecordedMetrics(), "should have no metrics initially")

	// Record an operation
	recorder.RecordOperation("test", "success")
	assert.True(t, recorder.HasRecordedMetrics(), "should have metrics after recording operation")

	// Reset and check again
	recorder.Reset()
	assert.False(t, recorder.HasRecordedMetrics(), "should have no metrics after reset")

	// Record a duration
	recorder.RecordDuration("test", 0.1)
	assert.True(t, recorder.HasRecordedMetrics(), "should have metrics after recording duration")

	// Reset and record an error
	recorder.Reset()
	recorder.RecordError("test", "error")
	assert.True(t, recorder.HasRecordedMetrics(), "should have metrics after recording error")
}

// TestNoOpRecorder verifies that the NoOpRecorder correctly implements the Recorder interface.
func TestNoOpRecorder(t *testing.T) {
	t.Parallel()

	recorder := NewNoOpRecorder()

	// These operations should not panic and should do nothing
	recorder.RecordOperation("test", "success")
	recorder.RecordDuration("test", 0.123)
	recorder.RecordError("test", "error")

	// No assertions needed - just verify no panics occur
}

// TestRecorderWithRealMetrics verifies that real metrics types implement the Recorder interface.
func TestRecorderWithRealMetrics(t *testing.T) {
	t.Parallel()

	t.Run("BirdNETMetrics", func(t *testing.T) {
		// This test verifies that BirdNETMetrics implements Recorder
		var _ Recorder = (*BirdNETMetrics)(nil)
	})

	t.Run("DatastoreMetrics", func(t *testing.T) {
		// This test verifies that DatastoreMetrics implements Recorder
		var _ Recorder = (*DatastoreMetrics)(nil)
	})
}

// BenchmarkTestRecorder benchmarks the TestRecorder implementation.
func BenchmarkTestRecorder(b *testing.B) {
	b.Run("RecordOperation", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Setup: create a fresh recorder for each run
		recorder := NewTestRecorder()

		b.StartTimer() // Start timer after setup is complete

		// Run the benchmark
		for b.Loop() {
			recorder.RecordOperation("bench", "success")
		}

		b.StopTimer() // Stop timer before any cleanup
	})

	b.Run("RecordDuration", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Setup: create a fresh recorder
		recorder := NewTestRecorder()

		b.StartTimer() // Start timer after setup

		// Run the benchmark
		for b.Loop() {
			recorder.RecordDuration("bench", 0.123)
		}

		b.StopTimer() // Stop timer after benchmark
	})

	b.Run("RecordError", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Setup: create a fresh recorder
		recorder := NewTestRecorder()

		b.StartTimer() // Start timer after setup

		// Run the benchmark
		for b.Loop() {
			recorder.RecordError("bench", "error")
		}

		b.StopTimer() // Stop timer after benchmark
	})

	b.Run("GetOperationCount", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Setup: create and populate recorder
		recorder := NewTestRecorder()
		recorder.RecordOperation("bench", "success")

		b.StartTimer() // Start timer after all setup is complete

		// Run the benchmark
		for b.Loop() {
			_ = recorder.GetOperationCount("bench", "success")
		}

		b.StopTimer() // Stop timer after benchmark
	})

	b.Run("ConcurrentOperations", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Setup: create a fresh recorder
		recorder := NewTestRecorder()

		b.StartTimer() // Start timer after setup

		// Run concurrent operations
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				recorder.RecordOperation("bench", "success")
			}
		})

		b.StopTimer() // Stop timer after benchmark
	})

	// Benchmark with recorder reset between iterations
	b.Run("RecordOperationWithReset", func(b *testing.B) {
		b.StopTimer() // Stop timer before setup

		// Create recorder once
		recorder := NewTestRecorder()

		// Run the benchmark with reset
		for b.Loop() {
			b.StopTimer()    // Stop timer before reset
			recorder.Reset() // Reset state between iterations
			b.StartTimer()   // Restart timer after reset

			recorder.RecordOperation("bench", "success")
		}

		b.StopTimer() // Final stop
	})
}

// ExampleTestRecorder demonstrates how to use the TestRecorder in tests.
func ExampleTestRecorder() {
	recorder := NewTestRecorder()

	// Record some operations
	recorder.RecordOperation("prediction", "success")
	recorder.RecordDuration("prediction", 0.123)

	// Later in the test, verify the operations
	successCount := recorder.GetOperationCount("prediction", "success")
	durations := recorder.GetDurations("prediction")

	_ = successCount // Use in assertions
	_ = durations    // Use in assertions
}

// TestRecorderUsageExample shows how components can use the Recorder interface.
func TestRecorderUsageExample(t *testing.T) {
	// This example shows how a component would use the Recorder interface
	type Component struct {
		metrics Recorder
	}

	doWork := func(c *Component, simulatedDuration time.Duration) error {
		// Record the simulated duration instead of actual elapsed time
		defer func() {
			c.metrics.RecordDuration("work", simulatedDuration.Seconds())
		}()

		// In real code, work would happen here
		// For testing, we just use the simulated duration

		// Record success
		c.metrics.RecordOperation("work", "success")
		return nil
	}

	// Test with TestRecorder
	testRecorder := NewTestRecorder()
	component := &Component{metrics: testRecorder}

	// Use a fixed duration for deterministic testing
	simulatedDuration := 15 * time.Millisecond

	err := doWork(component, simulatedDuration)
	require.NoError(t, err, "doWork should not return an error")

	// Verify metrics were recorded
	assert.Equal(t, 1, testRecorder.GetOperationCount("work", "success"),
		"should have 1 successful operation")

	durations := testRecorder.GetDurations("work")
	require.Len(t, durations, 1, "should have 1 duration recorded")
	assert.InDelta(t, simulatedDuration.Seconds(), durations[0], 0.01,
		"recorded duration should match simulated duration")
}
