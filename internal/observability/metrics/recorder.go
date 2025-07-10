// Package metrics provides custom Prometheus metrics for the BirdNET-Go application.
package metrics

// Recorder defines a minimal interface for recording metrics.
// This interface improves testability by allowing components to depend on
// an abstraction rather than concrete metric implementations.
type Recorder interface {
	// RecordOperation records a generic operation with its status.
	// The operation parameter describes what was performed (e.g., "prediction", "model_load").
	// The status parameter indicates the outcome (e.g., "success", "error").
	RecordOperation(operation, status string)

	// RecordDuration records the duration of an operation in seconds.
	// The operation parameter describes what was measured (e.g., "chunk_process", "range_filter").
	RecordDuration(operation string, seconds float64)

	// RecordError records an error occurrence with its type.
	// The operation parameter describes where the error occurred.
	// The errorType parameter categorizes the error (e.g., "validation", "io", "model").
	RecordError(operation, errorType string)
}
