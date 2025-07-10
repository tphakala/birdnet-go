package metrics

import (
	"testing"
	"time"
)

// TestRecordOperation verifies RecordOperation functionality of TestRecorder.
func TestRecordOperation(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordOperation("prediction", "success")
	recorder.RecordOperation("prediction", "success")
	recorder.RecordOperation("prediction", "error")
	recorder.RecordOperation("model_load", "success")

	if count := recorder.GetOperationCount("prediction", "success"); count != 2 {
		t.Errorf("expected 2 successful predictions, got %d", count)
	}
	if count := recorder.GetOperationCount("prediction", "error"); count != 1 {
		t.Errorf("expected 1 error prediction, got %d", count)
	}
	if count := recorder.GetOperationCount("model_load", "success"); count != 1 {
		t.Errorf("expected 1 successful model load, got %d", count)
	}
	if count := recorder.GetOperationCount("model_load", "error"); count != 0 {
		t.Errorf("expected 0 error model loads, got %d", count)
	}
}

// TestRecordDuration verifies RecordDuration functionality of TestRecorder.
func TestRecordDuration(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordDuration("prediction", 0.123)
	recorder.RecordDuration("prediction", 0.456)
	recorder.RecordDuration("chunk_process", 0.789)

	predDurations := recorder.GetDurations("prediction")
	if len(predDurations) != 2 {
		t.Fatalf("expected 2 prediction durations, got %d", len(predDurations))
	}
	if predDurations[0] != 0.123 || predDurations[1] != 0.456 {
		t.Errorf("unexpected prediction durations: %v", predDurations)
	}

	chunkDurations := recorder.GetDurations("chunk_process")
	if len(chunkDurations) != 1 {
		t.Fatalf("expected 1 chunk process duration, got %d", len(chunkDurations))
	}
	if chunkDurations[0] != 0.789 {
		t.Errorf("expected chunk duration 0.789, got %f", chunkDurations[0])
	}

	// Test non-existent operation
	if durations := recorder.GetDurations("non_existent"); durations != nil {
		t.Errorf("expected nil for non-existent operation, got %v", durations)
	}
}

// TestRecordError verifies RecordError functionality of TestRecorder.
func TestRecordError(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordError("prediction", "validation")
	recorder.RecordError("prediction", "validation")
	recorder.RecordError("prediction", "model_error")
	recorder.RecordError("db_query", "connection")

	if count := recorder.GetErrorCount("prediction", "validation"); count != 2 {
		t.Errorf("expected 2 validation errors, got %d", count)
	}
	if count := recorder.GetErrorCount("prediction", "model_error"); count != 1 {
		t.Errorf("expected 1 model error, got %d", count)
	}
	if count := recorder.GetErrorCount("db_query", "connection"); count != 1 {
		t.Errorf("expected 1 connection error, got %d", count)
	}
	if count := recorder.GetErrorCount("db_query", "timeout"); count != 0 {
		t.Errorf("expected 0 timeout errors, got %d", count)
	}
}

// TestRecorderThreadSafety verifies thread safety of TestRecorder.
func TestRecorderThreadSafety(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	done := make(chan bool)
	numGoroutines := 10
	opsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < opsPerGoroutine; j++ {
				recorder.RecordOperation("concurrent", "success")
				recorder.RecordDuration("concurrent", 0.001)
				recorder.RecordError("concurrent", "test")
			}
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	expectedCount := numGoroutines * opsPerGoroutine
	if count := recorder.GetOperationCount("concurrent", "success"); count != expectedCount {
		t.Errorf("expected %d operations after concurrent access, got %d", expectedCount, count)
	}
	if durations := recorder.GetDurations("concurrent"); len(durations) != expectedCount {
		t.Errorf("expected %d durations after concurrent access, got %d", expectedCount, len(durations))
	}
	if count := recorder.GetErrorCount("concurrent", "test"); count != expectedCount {
		t.Errorf("expected %d errors after concurrent access, got %d", expectedCount, count)
	}
}

// TestGetAllOperations verifies GetAllOperations functionality of TestRecorder.
func TestGetAllOperations(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordOperation("op1", "success")
	recorder.RecordOperation("op1", "error")
	recorder.RecordOperation("op2", "success")

	all := recorder.GetAllOperations()
	if len(all) != 2 {
		t.Errorf("expected 2 operations, got %d", len(all))
	}
	if all["op1"]["success"] != 1 || all["op1"]["error"] != 1 {
		t.Errorf("unexpected op1 counts: %v", all["op1"])
	}
	if all["op2"]["success"] != 1 {
		t.Errorf("unexpected op2 counts: %v", all["op2"])
	}
}

// TestGetAllErrors verifies GetAllErrors functionality of TestRecorder.
func TestGetAllErrors(t *testing.T) {
	t.Parallel()

	recorder := NewTestRecorder()
	recorder.RecordError("op1", "type1")
	recorder.RecordError("op1", "type2")
	recorder.RecordError("op2", "type1")

	all := recorder.GetAllErrors()
	if len(all) != 2 {
		t.Errorf("expected 2 operations with errors, got %d", len(all))
	}
	if all["op1"]["type1"] != 1 || all["op1"]["type2"] != 1 {
		t.Errorf("unexpected op1 error counts: %v", all["op1"])
	}
	if all["op2"]["type1"] != 1 {
		t.Errorf("unexpected op2 error counts: %v", all["op2"])
	}
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
	recorder := NewTestRecorder()

	b.Run("RecordOperation", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			recorder.RecordOperation("bench", "success")
		}
	})

	b.Run("RecordDuration", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			recorder.RecordDuration("bench", 0.123)
		}
	})

	b.Run("RecordError", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			recorder.RecordError("bench", "error")
		}
	})

	b.Run("GetOperationCount", func(b *testing.B) {
		recorder.RecordOperation("bench", "success")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = recorder.GetOperationCount("bench", "success")
		}
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

	doWork := func(c *Component) error {
		start := time.Now()
		defer func() {
			c.metrics.RecordDuration("work", time.Since(start).Seconds())
		}()

		// Simulate some work
		time.Sleep(10 * time.Millisecond)

		// Record success
		c.metrics.RecordOperation("work", "success")
		return nil
	}

	// Test with TestRecorder
	testRecorder := NewTestRecorder()
	component := &Component{metrics: testRecorder}

	if err := doWork(component); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify metrics were recorded
	if count := testRecorder.GetOperationCount("work", "success"); count != 1 {
		t.Errorf("expected 1 successful operation, got %d", count)
	}

	durations := testRecorder.GetDurations("work")
	if len(durations) != 1 {
		t.Fatalf("expected 1 duration, got %d", len(durations))
	}
	if durations[0] < 0.01 {
		t.Errorf("expected duration >= 0.01s, got %f", durations[0])
	}
}