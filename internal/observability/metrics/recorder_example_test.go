package metrics_test

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// ExampleComponent demonstrates how a component can use the Recorder interface
// instead of concrete metric types for improved testability.
type ExampleComponent struct {
	// Using the interface instead of a concrete metric type
	metrics metrics.Recorder
}

// NewExampleComponent creates a new component with a metrics recorder.
// In production, pass a real metrics implementation (e.g., BirdNETMetrics).
// In tests, pass a TestRecorder or NoOpRecorder.
func NewExampleComponent(recorder metrics.Recorder) *ExampleComponent {
	return &ExampleComponent{
		metrics: recorder,
	}
}

// DoWork demonstrates how the component uses the Recorder interface.
func (c *ExampleComponent) DoWork() error {
	start := time.Now()

	// Record that we're starting an operation
	c.metrics.RecordOperation("example_work", "started")

	// Simulate some work
	time.Sleep(100 * time.Millisecond)

	// Check for errors (simulated)
	if false { // This would be a real error check
		c.metrics.RecordError("example_work", "validation")
		c.metrics.RecordOperation("example_work", "error")
		return fmt.Errorf("work failed")
	}

	// Record success and duration
	c.metrics.RecordOperation("example_work", "success")
	c.metrics.RecordDuration("example_work", time.Since(start).Seconds())

	return nil
}

// Example_componentWithRecorder shows how to use the Recorder interface in practice.
func Example_componentWithRecorder() {
	// In production: use a real metrics implementation
	// component := NewExampleComponent(realMetrics)

	// In tests: use a test recorder
	testRecorder := metrics.NewTestRecorder()
	component := NewExampleComponent(testRecorder)

	// Do some work
	_ = component.DoWork()

	// Verify metrics were recorded (in tests)
	fmt.Printf("Operations recorded: %d\n", testRecorder.GetOperationCount("example_work", "success"))
	durations := testRecorder.GetDurations("example_work")
	fmt.Printf("Durations recorded: %d\n", len(durations))

	// Output:
	// Operations recorded: 1
	// Durations recorded: 1
}

// Example_migrationPath shows how to migrate existing code to use the Recorder interface.
func Example_migrationPath() {
	// Before: Component with concrete metric type
	type OldComponent struct {
		metrics *metrics.BirdNETMetrics
	}

	// After: Component with Recorder interface
	type NewComponent struct {
		metrics metrics.Recorder
	}

	// The migration is simple:
	// 1. Change the field type from concrete to interface
	// 2. Update constructor/SetMetrics to accept the interface
	// 3. No changes needed to metric recording calls

	// Both approaches work with the same metrics instance:
	// var birdnetMetrics *metrics.BirdNETMetrics
	// oldComp := &OldComponent{metrics: birdnetMetrics}
	// newComp := &NewComponent{metrics: birdnetMetrics} // BirdNETMetrics implements Recorder

	fmt.Println("Migration complete")
	// Output: Migration complete
}
