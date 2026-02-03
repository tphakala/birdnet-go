package jobqueue

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetryBackoffWithMockClock demonstrates how to test the retry backoff mechanism
// using a mock clock for more precise control over time.
func TestRetryBackoffWithMockClock(t *testing.T) {
	// Skip this test in normal test runs as it's just a demonstration
	t.Skip("This test is a demonstration of using MockClock and is not meant to be run regularly")

	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, 10, false)
	defer teardownTestQueue(t, queue)

	// Create a mock clock with a known start time
	startTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	mockClock := NewMockClock(startTime)
	queue.SetClock(mockClock)

	// Create a channel to track execution times
	var executionTimes []time.Time
	executionDone := make(chan struct{})

	// Maximum number of retries
	maxRetries := 2

	// Create a mock action that always fails and records execution times
	action := &MockAction{
		Description: "Backoff Test Action",
		ExecuteFunc: func(data any) error {
			currentTime := mockClock.Now()
			t.Logf("Execution at %v", currentTime)

			// Record the execution time
			executionTimes = append(executionTimes, currentTime)

			// Signal that an execution has occurred
			close(executionDone)

			// Create a new channel for the next execution
			executionDone = make(chan struct{})

			return errors.New("simulated failure")
		},
	}

	// Create test data
	data := &TestData{ID: "backoff-test"}

	// Create retry config with specific backoff parameters
	initialDelay := 20 * time.Millisecond
	multiplier := 2.0
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   maxRetries,
		InitialDelay: initialDelay,
		MaxDelay:     200 * time.Millisecond,
		Multiplier:   multiplier,
	}

	// Enqueue the job
	job, err := queue.Enqueue(ctx, action, data, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Process the job initially
	queue.ProcessImmediately(ctx)

	// Wait for the first execution
	<-executionDone
	t.Logf("First execution at %v", executionTimes[0])

	// Calculate the expected second retry time
	secondDelayDuration := time.Duration(float64(initialDelay) * multiplier)

	// Advance the mock clock past the first retry delay
	mockClock.Advance(initialDelay + 1*time.Millisecond)

	// Process the job for the first retry
	queue.ProcessImmediately(ctx)

	// Wait for the second execution
	<-executionDone
	t.Logf("Second execution at %v", executionTimes[1])

	// Advance the mock clock past the second retry delay
	mockClock.Advance(secondDelayDuration + 1*time.Millisecond)

	// Process the job for the second retry
	queue.ProcessImmediately(ctx)

	// Wait for the third execution
	<-executionDone
	t.Logf("Third execution at %v", executionTimes[2])

	// Check that we have the expected number of execution times
	expectedExecutions := maxRetries + 1 // Initial attempt + retries
	assert.Len(t, expectedExecutions, len(executionTimes), "Should have %d execution times", expectedExecutions)

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.TotalJobs, "Total jobs should be 1")
	assert.Equal(t, 0, stats.SuccessfulJobs, "Successful jobs should be 0")
	assert.Equal(t, 1, stats.FailedJobs, "Failed jobs should be 1")
	assert.Equal(t, maxRetries+1, stats.RetryAttempts, "Retry attempts should match maxRetries + 1")

	// Calculate the expected retry times
	expectedTimes := []time.Time{
		startTime,                   // Initial execution
		startTime.Add(initialDelay), // First retry
		startTime.Add(initialDelay).Add(secondDelayDuration), // Second retry
	}

	// Check that the execution times match the expected times
	for i := range len(executionTimes) {
		timeDiff := executionTimes[i].Sub(expectedTimes[i])
		t.Logf("Execution %d: Expected %v, Got %v, Diff %v", i+1, expectedTimes[i], executionTimes[i], timeDiff)

		// The difference should be small, accounting for jitter
		assert.Less(t, timeDiff, 5*time.Millisecond,
			"Execution time %d should be close to expected time: expected %v, got %v",
			i+1, expectedTimes[i], executionTimes[i])
	}
}
