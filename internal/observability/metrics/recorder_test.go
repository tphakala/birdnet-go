// Package metrics provides custom Prometheus metrics for the BirdNET-Go application.
package metrics

import (
	"sync"
)

// TestRecorder is a test implementation of the Recorder interface.
// It captures all recorded metrics for verification in tests.
type TestRecorder struct {
	mu         sync.RWMutex
	operations map[string]map[string]int // operation -> status -> count
	durations  map[string][]float64      // operation -> list of durations
	errors     map[string]map[string]int // operation -> errorType -> count
}

// NewTestRecorder creates a new test recorder instance.
func NewTestRecorder() *TestRecorder {
	return &TestRecorder{
		operations: make(map[string]map[string]int),
		durations:  make(map[string][]float64),
		errors:     make(map[string]map[string]int),
	}
}

// RecordOperation implements the Recorder interface for testing.
func (r *TestRecorder) RecordOperation(operation, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.operations[operation] == nil {
		r.operations[operation] = make(map[string]int)
	}
	r.operations[operation][status]++
}

// RecordDuration implements the Recorder interface for testing.
func (r *TestRecorder) RecordDuration(operation string, seconds float64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.durations[operation] = append(r.durations[operation], seconds)
}

// RecordError implements the Recorder interface for testing.
func (r *TestRecorder) RecordError(operation, errorType string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.errors[operation] == nil {
		r.errors[operation] = make(map[string]int)
	}
	r.errors[operation][errorType]++
}

// GetOperationCount returns the count of a specific operation and status.
func (r *TestRecorder) GetOperationCount(operation, status string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if statusMap, ok := r.operations[operation]; ok {
		return statusMap[status]
	}
	return 0
}

// GetDurations returns all recorded durations for a specific operation.
func (r *TestRecorder) GetDurations(operation string) []float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if durations, ok := r.durations[operation]; ok {
		// Return a copy to prevent external modification
		result := make([]float64, len(durations))
		copy(result, durations)
		return result
	}
	return nil
}

// GetErrorCount returns the count of a specific error type for an operation.
func (r *TestRecorder) GetErrorCount(operation, errorType string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if errorMap, ok := r.errors[operation]; ok {
		return errorMap[errorType]
	}
	return 0
}

// Reset clears all recorded metrics.
func (r *TestRecorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.operations = make(map[string]map[string]int)
	r.durations = make(map[string][]float64)
	r.errors = make(map[string]map[string]int)
}

// GetAllOperations returns a copy of all recorded operations.
func (r *TestRecorder) GetAllOperations() map[string]map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Deep copy to prevent external modification
	result := make(map[string]map[string]int)
	for op, statusMap := range r.operations {
		result[op] = make(map[string]int)
		for status, count := range statusMap {
			result[op][status] = count
		}
	}
	return result
}

// GetAllErrors returns a copy of all recorded errors.
func (r *TestRecorder) GetAllErrors() map[string]map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Deep copy to prevent external modification
	result := make(map[string]map[string]int)
	for op, errorMap := range r.errors {
		result[op] = make(map[string]int)
		for errType, count := range errorMap {
			result[op][errType] = count
		}
	}
	return result
}

// HasRecordedMetrics returns true if any metrics have been recorded.
// This is useful for negative tests to verify that no metrics were recorded.
func (r *TestRecorder) HasRecordedMetrics() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.operations) > 0 || len(r.durations) > 0 || len(r.errors) > 0
}

// NoOpRecorder is a no-op implementation of the Recorder interface.
// It can be used when metrics recording is not needed.
type NoOpRecorder struct{}

// RecordOperation does nothing.
func (n *NoOpRecorder) RecordOperation(operation, status string) {}

// RecordDuration does nothing.
func (n *NoOpRecorder) RecordDuration(operation string, seconds float64) {}

// RecordError does nothing.
func (n *NoOpRecorder) RecordError(operation, errorType string) {}

// NewNoOpRecorder creates a new no-op recorder instance.
func NewNoOpRecorder() *NoOpRecorder {
	return &NoOpRecorder{}
}
