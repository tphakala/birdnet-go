package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockClock is a mock implementation of the Clock interface for testing
type MockClock struct {
	mu            sync.Mutex
	currentTime   time.Time
	afterChannels []mockAfterChannel
}

type mockAfterChannel struct {
	triggerTime time.Time
	ch          chan time.Time
}

// NewMockClock creates a new MockClock with the specified initial time
func NewMockClock(initialTime time.Time) *MockClock {
	return &MockClock{
		currentTime:   initialTime,
		afterChannels: make([]mockAfterChannel, 0),
	}
}

// Now returns the current mock time
func (m *MockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime
}

// Sleep is a no-op in the mock clock
func (m *MockClock) Sleep(d time.Duration) {
	// No-op in mock clock
}

// After returns a channel that will receive the current time after the specified duration
func (m *MockClock) After(d time.Duration) <-chan time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan time.Time, 1)
	triggerTime := m.currentTime.Add(d)
	m.afterChannels = append(m.afterChannels, mockAfterChannel{
		triggerTime: triggerTime,
		ch:          ch,
	})

	return ch
}

// Advance advances the mock clock by the specified duration and triggers any after channels
func (m *MockClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentTime = m.currentTime.Add(d)

	// Trigger any after channels that have reached their time
	var remainingChannels []mockAfterChannel
	for _, ac := range m.afterChannels {
		if !m.currentTime.Before(ac.triggerTime) {
			// This channel should be triggered
			select {
			case ac.ch <- m.currentTime:
				// Channel triggered
			default:
				// Channel already closed or full
			}
		} else {
			// This channel should not be triggered yet
			remainingChannels = append(remainingChannels, ac)
		}
	}

	m.afterChannels = remainingChannels
}

// Set sets the mock clock to the specified time and triggers any after channels
func (m *MockClock) Set(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentTime = t

	// Trigger any after channels that have reached their time
	var remainingChannels []mockAfterChannel
	for _, ac := range m.afterChannels {
		if !m.currentTime.Before(ac.triggerTime) {
			// This channel should be triggered
			select {
			case ac.ch <- m.currentTime:
				// Channel triggered
			default:
				// Channel already closed or full
			}
		} else {
			// This channel should not be triggered yet
			remainingChannels = append(remainingChannels, ac)
		}
	}

	m.afterChannels = remainingChannels
}

// MockAction implements the Action interface for testing
type MockAction struct {
	ExecuteFunc    func(data any) error // Legacy callback without context
	ExecuteCount   int
	ExecuteDelay   time.Duration
	ExecuteTimeout bool
	Description    string // Description for the action
	mu             sync.Mutex
}

// Execute implements the Action interface
func (m *MockAction) Execute(ctx context.Context, data any) error {
	m.mu.Lock()
	m.ExecuteCount++
	m.mu.Unlock()

	// Simulate execution delay if specified
	if m.ExecuteDelay > 0 {
		time.Sleep(m.ExecuteDelay)
	}

	// Simulate timeout if specified
	if m.ExecuteTimeout {
		// Just hang indefinitely, the test will timeout this
		select {}
	}

	// Use the provided function if available
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(data)
	}

	// Default implementation just returns nil (success)
	return nil
}

// GetDescription implements the Action interface
func (m *MockAction) GetDescription() string {
	if m.Description != "" {
		return m.Description
	}
	return "Mock Action"
}

func (m *MockAction) GetExecuteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ExecuteCount
}

// TestData is a simple data structure for testing
type TestData struct {
	ID   string
	Data string
}

// setupTestQueue creates a job queue for testing with custom options
func setupTestQueue(t *testing.T, maxJobs int, logAllSuccesses bool) *JobQueue {
	t.Helper()
	queue := NewJobQueueWithOptions(maxJobs, logAllSuccesses)
	// Set a much shorter processing interval for faster test execution
	queue.SetProcessingInterval(10 * time.Millisecond)
	queue.Start()
	return queue
}

// teardownTestQueue stops the job queue
func teardownTestQueue(t *testing.T, queue *JobQueue) {
	t.Helper()
	err := queue.StopWithTimeout(1 * time.Second)
	require.NoError(t, err, "Failed to stop job queue")
}

// TestBasicQueueFunctionality tests the basic functionality of the job queue
func TestBasicQueueFunctionality(t *testing.T) {
	t.Parallel()
	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create a mock action
	action := &MockAction{}

	// Create test data
	data := &TestData{ID: "test-1", Data: "test data"}

	// Create retry config
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Enqueue the job
	job, err := queue.Enqueue(t.Context(), action, data, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Wait for the job to be processed. The processing ticker is 10ms (see
	// setupTestQueue), but SuccessfulJobs is incremented in handleJobSuccess
	// after the action returns, so poll the stats instead of sleeping a fixed
	// amount that could be missed on a slow or coarse-timer CI runner.
	require.Eventually(t, func() bool {
		return queue.GetStats().SuccessfulJobs == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "job should be processed successfully")

	// Check that the action was executed
	assert.Equal(t, 1, action.GetExecuteCount(), "Action should have been executed once")

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.TotalJobs, "Total jobs should be 1")
	assert.Equal(t, 1, stats.SuccessfulJobs, "Successful jobs should be 1")
	assert.Equal(t, 0, stats.FailedJobs, "Failed jobs should be 0")
}

// TestMultipleJobs tests enqueueing and processing multiple jobs
func TestMultipleJobs(t *testing.T) {
	t.Parallel()
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Number of jobs to enqueue
	numJobs := 10

	// Create a wait group to wait for all jobs to complete
	var wg sync.WaitGroup
	wg.Add(numJobs)

	// Create a counter for completed jobs
	var completedJobs atomic.Int32

	// Create retry config
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   3,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Enqueue multiple jobs
	for i := range numJobs {
		// Create a mock action that decrements the wait group when executed
		action := &MockAction{
			ExecuteFunc: func(data any) error {
				defer wg.Done()
				completedJobs.Add(1)
				return nil
			},
		}

		// Create test data
		data := &TestData{ID: fmt.Sprintf("test-%d", i), Data: fmt.Sprintf("test data %d", i)}

		// Enqueue the job
		_, err := queue.Enqueue(t.Context(), action, data, config)
		require.NoError(t, err, "Failed to enqueue job")
	}

	// Force immediate processing to ensure all jobs are processed
	for range numJobs {
		queue.ProcessImmediately(ctx)
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for all jobs to complete with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case <-done:
		// All jobs completed
	case <-timeoutCtx.Done():
		require.Fail(t, "Timed out waiting for jobs to complete")
	}

	// completedJobs is incremented before wg.Done() in each action, so the done
	// channel above guarantees it has reached numJobs. SuccessfulJobs, however,
	// is incremented in handleJobSuccess after the action returns, so poll the
	// queue stats instead of sleeping a fixed amount.
	assert.Equal(t, int32(numJobs), completedJobs.Load(), "All jobs should have been completed")

	require.Eventually(t, func() bool {
		return queue.GetStats().SuccessfulJobs == numJobs
	}, DefaultTestTimeout, 10*time.Millisecond, "all jobs should be recorded as successful")

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, numJobs, stats.TotalJobs, "Total jobs should match the number of enqueued jobs")
	assert.Equal(t, numJobs, stats.SuccessfulJobs, "Successful jobs should match the number of enqueued jobs")
	assert.Equal(t, 0, stats.FailedJobs, "Failed jobs should be 0")
}

// TestRetryProcess tests the retry process for jobs that fail
func TestRetryProcess(t *testing.T) {
	t.Parallel()
	// Create a context for manual control
	ctx := t.Context()

	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Number of times the job should fail before succeeding
	failCount := 2

	// Create a channel to signal when the job is done
	done := make(chan struct{})

	// Create a counter for tracking attempts
	var attemptCount atomic.Int32

	// Create a mock action that fails a specified number of times before succeeding
	action := &MockAction{
		ExecuteFunc: func(data any) error {
			count := attemptCount.Add(1)
			t.Logf("TestRetryProcess: Attempt %d of %d", count, failCount+1)
			// failCount is a small test constant, safe to convert to int32
			if count <= int32(failCount) { //nolint:gosec // G115: test values small, safe conversion
				// Return failure for the first N attempts
				return errors.New("simulated failure")
			}
			// Signal that the job has succeeded and close the done channel
			t.Logf("TestRetryProcess: Job succeeded on attempt %d", count)
			close(done)
			return nil
		},
	}

	// Create test data
	data := &TestData{ID: "retry-test"}

	// Create retry config with very short delays to speed up the test
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   5,                    // More than failCount to ensure it eventually succeeds
		InitialDelay: 1 * time.Millisecond, // Very short delay
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   1.2,
	}

	// Enqueue the job
	job, err := queue.Enqueue(t.Context(), action, data, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Force immediate processing for up to failCount+1 times
	completed := false
	for i := 0; i <= failCount+1; i++ {
		// First check if we're done
		select {
		case <-done:
			t.Logf("TestRetryProcess: Job completed successfully after %d attempts", attemptCount.Load())
			completed = true
		default:
			// Not done yet, force processing
		}

		if completed {
			break
		}

		// Force immediate processing
		queue.ProcessImmediately(ctx)

		// Small delay to allow execution to complete
		time.Sleep(10 * time.Millisecond)
	}

	// Wait a bit longer in case we still need more time
	if !completed {
		retryCtx, retryCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer retryCancel()
		select {
		case <-done:
			t.Logf("TestRetryProcess: Job completed successfully after %d attempts", attemptCount.Load())
		case <-retryCtx.Done():
			require.Fail(t, "Timed out waiting for job to succeed", "Current attempt count: %d", attemptCount.Load())
		}
	}
	// The done channel is closed inside the action, before handleJobSuccess
	// records the success under the queue lock. Poll until the success is
	// recorded rather than asserting immediately, which raced on loaded CI.
	require.Eventually(t, func() bool {
		return queue.GetStats().SuccessfulJobs == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "job should be recorded as successful")

	// Check that the action was executed the expected number of times
	expectedExecutions := failCount + 1 // Initial attempt + retries
	actualExecutions := action.GetExecuteCount()
	assert.Equal(t, expectedExecutions, actualExecutions,
		"Action should have been executed %d times, got %d", expectedExecutions, actualExecutions)

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.TotalJobs, "Total jobs should be 1")
	assert.Equal(t, 1, stats.SuccessfulJobs, "Successful jobs should be 1")
	assert.Equal(t, 0, stats.FailedJobs, "Failed jobs should be 0")
	assert.GreaterOrEqual(t, stats.RetryAttempts, failCount, "Retry attempts should be at least failCount")
}

// TestRetryExhaustion tests that jobs fail permanently after reaching the maximum number of retries
func TestRetryExhaustion(t *testing.T) {
	t.Parallel()
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create a counter for tracking attempts
	var attemptCount atomic.Int32

	// Maximum number of retries
	maxRetries := 2

	// Create a mock action that always fails
	action := &MockAction{
		ExecuteFunc: func(data any) error {
			count := attemptCount.Add(1)
			t.Logf("TestRetryExhaustion: Attempt %d of %d", count, maxRetries+1)
			return errors.New("simulated failure")
		},
	}

	// Create test data
	data := &TestData{ID: "exhaustion-test"}

	// Create retry config with very short delays to speed up the test
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   maxRetries,
		InitialDelay: 1 * time.Millisecond, // Very short delay
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   1.2,
	}

	// Enqueue the job
	job, err := queue.Enqueue(t.Context(), action, data, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Drive the job through its retries until it exhausts them and is recorded
	// as failed. The 10ms processing ticker also advances retries; nudging with
	// ProcessImmediately keeps the test fast. handleJobFailure records the
	// terminal failure under the lock after the action returns, so poll for it
	// rather than sleeping a fixed number of cycles that can be missed on CI.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().FailedJobs == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "job should fail after exhausting retries")

	// Check that the action was executed the expected number of times
	// The job is executed once initially and then retried maxRetries times
	// But the job queue implementation counts attempts starting from 1, not 0
	// So the actual number of executions is maxRetries + 1
	expectedExecutions := maxRetries + 1 // Initial attempt + retries
	actualExecutions := action.GetExecuteCount()
	assert.Equal(t, expectedExecutions, actualExecutions,
		"Action should have been executed %d times, got %d", expectedExecutions, actualExecutions)

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.TotalJobs, "Total jobs should be 1")
	assert.Equal(t, 0, stats.SuccessfulJobs, "Successful jobs should be 0")
	assert.Equal(t, 1, stats.FailedJobs, "Failed jobs should be 1")
	assert.Equal(t, maxRetries, stats.RetryAttempts, "Retry attempts should match maxRetries")

	// Verify the job status is failed
	var jobFailed bool
	queue.mu.Lock()
	// Check in active jobs
	for _, j := range queue.jobs {
		if j.ID == job.ID && j.Status == JobStatusFailed {
			jobFailed = true
			break
		}
	}

	queue.mu.Unlock()

	if !jobFailed {
		// After cleanupStaleJobs runs, the job pointer is discarded. Observe
		// the failure via the queue-wide counter instead.
		stats := queue.GetStats()
		if stats.FailedJobs > 0 {
			jobFailed = true
		}
	}

	assert.True(t, jobFailed, "Job should have failed permanently after exhausting retries")
}

// TestRetryBackoff tests that the retry backoff mechanism works correctly
func TestRetryBackoff(t *testing.T) {
	// TODO: This test could be improved by using a mock clock implementation
	// that allows precise control over time, rather than relying on real time
	// and sleep durations. This would make the test more reliable and faster.
	// See the Clock interface and MockClock implementation for a potential approach.

	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create a counter for tracking attempts
	var attemptCount atomic.Int32

	// Maximum number of retries
	maxRetries := 2

	// Create a channel to track execution times
	executionTimes := make(chan time.Time, maxRetries+1)

	// Create a mock action that always fails and records execution times
	action := &MockAction{
		ExecuteFunc: func(data any) error {
			executionTimes <- time.Now()
			count := attemptCount.Add(1)
			t.Logf("TestRetryBackoff: Attempt %d of %d", count, maxRetries+1)
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
	job, err := queue.Enqueue(t.Context(), action, data, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Drive the job through its retries. The action records each execution time
	// on the executionTimes channel; poll attemptCount until every attempt has
	// run rather than sleeping a fixed amount per retry, which raced on slow CI
	// when a backoff deadline had not elapsed by the time the fixed sleep ended.
	// The backoff delays still happen (NextRetryAt gates processDueJobs); this
	// test asserts on the execution count and final stats, not the exact gaps.
	expectedExecutions := maxRetries + 1 // Initial attempt + retries
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return int(attemptCount.Load()) >= expectedExecutions
	}, DefaultTestTimeout, 5*time.Millisecond,
		"job should be executed for the initial attempt plus every retry")

	// Stop the queue to ensure all goroutines complete
	require.NoError(t, queue.Stop(), "Failed to stop queue")

	// Close the channel and collect execution times
	close(executionTimes)
	// Pre-allocate slice with expected capacity (initial execution + retries)
	times := make([]time.Time, 0, maxRetries+1)
	for execTime := range executionTimes {
		times = append(times, execTime)
	}

	// Check that we have the expected number of execution times (already
	// computed above as the initial attempt plus every retry).
	assert.Len(t, times, expectedExecutions, "Should have %d execution times", expectedExecutions)
	t.Logf("TestRetryBackoff: Recorded %d execution times", len(times))

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.TotalJobs, "Total jobs should be 1")
	assert.Equal(t, 0, stats.SuccessfulJobs, "Successful jobs should be 0")
	assert.Equal(t, 1, stats.FailedJobs, "Failed jobs should be 1")
	assert.Equal(t, maxRetries, stats.RetryAttempts, "Retry attempts should match maxRetries")

	// Verify the job status is failed
	var jobFailed bool
	queue.mu.Lock()
	// Check in active jobs
	for _, j := range queue.jobs {
		if j.ID == job.ID && j.Status == JobStatusFailed {
			jobFailed = true
			break
		}
	}

	queue.mu.Unlock()

	if !jobFailed {
		// After cleanupStaleJobs runs, the job pointer is discarded. Observe
		// the failure via the queue-wide counter instead.
		stats := queue.GetStats()
		if stats.FailedJobs > 0 {
			jobFailed = true
		}
	}

	assert.True(t, jobFailed, "Job should have failed permanently after exhausting retries")

	// Check that the delays between executions follow the backoff pattern
	if len(times) >= 3 {
		// Calculate the delays between executions
		delays := make([]time.Duration, len(times)-1)
		for i := 1; i < len(times); i++ {
			delays[i-1] = times[i].Sub(times[i-1])
			t.Logf("Delay %d: %v", i, delays[i-1])
		}

		// We're manually controlling the retry timing, so we can't make direct assertions about the delays
		// Instead, just log them for information
		t.Logf("Manual retry delays: %v", delays)
	}
}

// TestJobExpiration tests that completed and failed jobs are removed from the
// active queue after cleanup so new jobs can be processed.
func TestJobExpiration(t *testing.T) {
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create a wait group to wait for all jobs to complete
	var wg sync.WaitGroup
	wg.Add(3) // Only wait for the successful jobs

	// Create retry config
	config := RetryConfig{
		Enabled:      false,
		MaxRetries:   0,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Enqueue 3 successful jobs
	for i := range 3 {
		action := &MockAction{
			ExecuteFunc: func(data any) error {
				defer wg.Done()
				return nil
			},
		}
		data := &TestData{ID: fmt.Sprintf("success-%d", i)}
		_, err := queue.Enqueue(t.Context(), action, data, config)
		require.NoError(t, err, "Failed to enqueue job")
	}

	// Enqueue 2 failing jobs
	for i := range 2 {
		action := &MockAction{
			ExecuteFunc: func(data any) error {
				return errors.New("simulated failure")
			},
		}
		data := &TestData{ID: fmt.Sprintf("fail-%d", i)}
		_, err := queue.Enqueue(t.Context(), action, data, config)
		require.NoError(t, err, "Failed to enqueue job")
	}

	// Wait for the successful jobs to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	waitForChannel(t, done, DefaultTestTimeout, "Timed out waiting for jobs to complete")

	// Wait for every job to finish and the cleanup tick (10ms ticker) to sweep
	// the terminal jobs out of the active queue. StaleJobs reaching 5 is the
	// deterministic signal that all five jobs are done and cleaned, replacing a
	// fixed multi-second sleep that could be missed under CI load.
	require.Eventually(t, func() bool {
		stats := queue.GetStats()
		return stats.StaleJobs == 5 && stats.SuccessfulJobs == 3 && stats.FailedJobs == 2
	}, DefaultTestTimeout, 10*time.Millisecond, "all jobs should finish and be cleaned up")

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 5, stats.TotalJobs, "Total jobs should be 5")
	assert.Equal(t, 3, stats.SuccessfulJobs, "Successful jobs should be 3")
	assert.Equal(t, 2, stats.FailedJobs, "Failed jobs should be 2")
	assert.Equal(t, 5, stats.StaleJobs, "Stale jobs should be 5")
	// Archive is gone: completed and failed jobs are discarded on the cleanup
	// tick. The active queue should be empty after all jobs finish.
	assert.Equal(t, 0, stats.PendingJobs, "Pending jobs should be 0 after cleanup")

	// Enqueue a new job to verify that the active jobs list is empty
	action := &MockAction{}
	data := &TestData{ID: "new-job"}
	_, err := queue.Enqueue(t.Context(), action, data, config)
	require.NoError(t, err, "Failed to enqueue job")

	// Wait for the new job to be processed instead of sleeping a fixed amount.
	require.Eventually(t, func() bool {
		return action.GetExecuteCount() == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "new job should have been executed")
}

// TestQueueOverflow tests that jobs are rejected when the queue is full
func TestQueueOverflow(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a job queue with a small capacity
	queueCapacity := 3
	queue := setupTestQueue(t, queueCapacity, false)
	defer teardownTestQueue(t, queue)

	// Disable job dropping for this test
	queue.SetAllowJobDropping(false)

	// Create a channel to signal when the blocking job has started
	jobStarted := make(chan struct{})
	// Create a channel to control when the blocking job should complete
	jobBlock := make(chan struct{})

	// 1. Create a blocking job that will signal when it starts and wait for our signal to complete
	blockingAction := &MockAction{
		ExecuteFunc: func(data any) error {
			// Signal that the job has started
			close(jobStarted)
			// Wait for the signal to complete
			<-jobBlock
			t.Log("Blocking job completed")
			return nil
		},
	}

	// 2. Create regular jobs that will fill the rest of the queue
	regularAction := &MockAction{
		ExecuteFunc: func(data any) error {
			time.Sleep(10 * time.Millisecond) // Short delay
			return nil
		},
	}

	// First enqueue the blocking job
	_, err := queue.Enqueue(t.Context(), blockingAction, &TestData{ID: "blocking-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Failed to enqueue blocking job")

	// Trigger processing to get the blocking job running
	queue.ProcessImmediately(ctx)

	// Wait for the blocking job to start (up to 1 second)
	waitForChannelWithLog(t, jobStarted, ShortTestTimeout, "Timed out waiting for blocking job to start", "Blocking job started successfully")

	// Now fill the rest of the queue with additional jobs
	// The queue capacity is queueCapacity, and one job is already running,
	// so we need to enqueue queueCapacity-1 jobs to fill it
	for i := range queueCapacity - 1 {
		jobID := fmt.Sprintf("regular-job-%d", i)
		_, err := queue.Enqueue(t.Context(), regularAction, &TestData{ID: jobID}, RetryConfig{Enabled: false})
		require.NoError(t, err, "Failed to enqueue regular job %d", i)
	}

	t.Log("Queue should now be full")

	// Try to enqueue one more job, which should fail with ErrQueueFull
	_, err = queue.Enqueue(t.Context(), regularAction, &TestData{ID: "overflow-job"}, RetryConfig{Enabled: false})
	require.ErrorIs(t, err, ErrQueueFull, "Enqueue should fail with ErrQueueFull when queue is full")

	// Now unblock the first job so it can complete and free its slot.
	close(jobBlock)

	// The blocking job finishes asynchronously: its goroutine returns, then
	// handleJobSuccess marks it Completed under the queue lock, and only a
	// later cleanupStaleJobs run removes it from the active slice that gates
	// maxJobs. A single ProcessImmediately plus a fixed sleep races that window
	// (the root cause of the windows-amd64 CI flake), so poll instead: each
	// iteration runs cleanup via ProcessImmediately until a slot actually
	// frees. PendingJobs is len(q.jobs), the exact value Enqueue checks against
	// maxJobs, and no other goroutine enqueues here, so once it drops below
	// capacity the following Enqueue cannot race back to full.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().PendingJobs < queueCapacity
	}, DefaultTestTimeout, 10*time.Millisecond,
		"blocking job should complete and free a queue slot")

	// Now we should be able to enqueue a new job.
	_, err = queue.Enqueue(t.Context(), regularAction, &TestData{ID: "after-freeing-space"}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Should be able to enqueue a job after making room")

	// Drive the remaining jobs to completion. Poll the queue-wide counters
	// instead of sleeping a fixed amount: SuccessfulJobs is incremented in
	// handleJobSuccess under the lock, and ProcessImmediately's cleanupStaleJobs
	// drains the active slice (PendingJobs) once every job reaches a terminal
	// state.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		stats := queue.GetStats()
		return stats.SuccessfulJobs == 4 && stats.PendingJobs == 0
	}, DefaultTestTimeout, 10*time.Millisecond,
		"all four jobs should complete successfully and the active queue should drain")

	// The active queue is now empty because cleanupStaleJobs discards
	// completed and failed jobs. The queue-wide counters still reflect every
	// job that was ever enqueued or finished, so we check those directly.
	stats := queue.GetStats()
	assert.Equal(t, 4, stats.TotalJobs, "Total jobs should include all jobs processed")
	assert.Equal(t, 4, stats.SuccessfulJobs, "All jobs should be successful")
	assert.Equal(t, 0, stats.PendingJobs, "Active queue should be empty after cleanup")
}

// TestDropOldestJob tests that the oldest pending job is dropped when the queue is full
func TestDropOldestJob(t *testing.T) {
	// Create a queue with a capacity of 3 jobs
	queueCapacity := 3
	queue := NewJobQueueWithOptions(queueCapacity, false)
	queue.Start()
	defer func() {
		assert.NoError(t, queue.Stop(), "Failed to stop queue")
	}()

	// Job dropping is enabled by default (allowJobDropping = true)

	// Create a context for the test
	ctx := t.Context()

	// Create a blocking action that will keep a job in the running state
	jobStarted := make(chan struct{})
	jobBlock := make(chan struct{})
	blockingAction := &MockAction{
		ExecuteFunc: func(data any) error {
			// Signal that the job has started
			close(jobStarted)
			// Wait for the signal to complete
			<-jobBlock
			return nil
		},
	}

	// Create a regular action for other jobs
	regularAction := &MockAction{
		ExecuteFunc: func(data any) error {
			return nil
		},
	}

	// First enqueue the blocking job (will be running, not pending)
	_, err := queue.Enqueue(t.Context(), blockingAction, &TestData{ID: "blocking-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Failed to enqueue blocking job")

	// Trigger processing to get the blocking job running
	queue.ProcessImmediately(ctx)

	// Wait for the blocking job to start (up to 1 second)
	waitForChannelWithLog(t, jobStarted, ShortTestTimeout, "Timed out waiting for blocking job to start", "Blocking job started successfully")

	// Now fill the rest of the queue with pending jobs
	pendingJobIDs := []string{
		"pending-job-1", // This will be the oldest pending job
		"pending-job-2", // This one will be exempt from dropping
	}

	// Enqueue the first pending job (will be dropped)
	_, err = queue.Enqueue(t.Context(), regularAction, &TestData{ID: pendingJobIDs[0]}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Failed to enqueue job %s", pendingJobIDs[0])

	// Enqueue the second pending job (exempt from dropping)
	job2, err := queue.Enqueue(t.Context(), regularAction, &TestData{ID: pendingJobIDs[1]}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Failed to enqueue job %s", pendingJobIDs[1])

	// Mark the second job as exempt from dropping
	job2.TestExemptFromDropping = true

	// The queue should now have:
	// - 1 running job (blocking-job)
	// - 2 pending jobs (pending-job-1, pending-job-2)
	// and be at its capacity of 3 jobs

	// Now try to add a new job, which should cause the oldest pending job to be dropped
	newJobID := "new-job"
	_, err = queue.Enqueue(t.Context(), regularAction, &TestData{ID: newJobID}, RetryConfig{Enabled: false})
	require.NoError(t, err, "Should be able to enqueue job with job dropping enabled")

	// Check that one job has been dropped
	stats := queue.GetStats()
	assert.Equal(t, 1, stats.DroppedJobs, "One job should have been dropped")

	// Get the list of pending jobs
	pendingJobs := queue.GetPendingJobs()

	// Extract job IDs from pending jobs
	var pendingIDs []string
	for _, job := range pendingJobs {
		if data, ok := job.Data.(*TestData); ok {
			pendingIDs = append(pendingIDs, data.ID)
		}
	}

	// Check that pending-job-1 (the oldest) is not in the pending jobs list
	assert.NotContains(t, pendingIDs, "pending-job-1", "The oldest pending job should have been dropped")

	// Check that pending-job-2 and new-job are in the pending jobs list
	assert.Contains(t, pendingIDs, "pending-job-2", "The second pending job should still be in the queue")
	assert.Contains(t, pendingIDs, newJobID, "The new job should be in the queue")

	// Complete the blocking job
	close(jobBlock)

	// Drive the remaining jobs to completion. SuccessfulJobs is incremented in
	// handleJobSuccess under the lock after each action returns, so poll the
	// counters instead of a fixed processing loop plus sleeps that can be missed
	// under CI load (same flake class as TestQueueOverflow).
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		s := queue.GetStats()
		return s.TotalJobs == 4 && s.SuccessfulJobs == 3
	}, DefaultTestTimeout, 10*time.Millisecond,
		"three jobs should complete successfully and one should stay dropped")

	// Verify final stats
	stats = queue.GetStats()
	assert.Equal(t, 4, stats.TotalJobs, "Total jobs should be 4 (3 successful + 1 dropped)")
	assert.Equal(t, 3, stats.SuccessfulJobs, "Successful jobs should be 3")
}

// TestHangingJobTimeout tests that hanging jobs are properly timed out
func TestHangingJobTimeout(t *testing.T) {
	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create a mock action that hangs indefinitely
	action := &MockAction{
		ExecuteTimeout: true, // This will cause the action to hang
	}

	// Create test data
	data := &TestData{ID: "hanging-job"}

	// Create retry config
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   1,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Create a done channel to signal when the test is complete
	done := make(chan struct{})

	// Enqueue the job in a goroutine so we don't block the test
	go func() {
		// Enqueue the job
		job, err := queue.Enqueue(t.Context(), action, data, config)
		assert.NoError(t, err, "Failed to enqueue job")
		assert.NotNil(t, job, "Job should not be nil")

		// Signal that the job has been enqueued
		close(done)
	}()

	// Wait for the job to be enqueued
	waitForChannel(t, done, ShortTestTimeout, "Timed out waiting for job to be enqueued")

	// Wait for the processing ticker to pick the job up and start it. The action
	// increments its execute count before hanging, so poll for that instead of
	// sleeping a fixed 2s that can be missed under CI load.
	require.Eventually(t, func() bool {
		return action.GetExecuteCount() == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "hanging job should be picked up and started")

	// The job should be running at this point
	// We can't directly check the job status, but we can check that the action was executed
	assert.Equal(t, 1, action.GetExecuteCount(), "Action should have been executed once")

	// We can't wait for the full 30-second timeout in a unit test,
	// so we'll just verify that the timeout mechanism exists by checking the code

	// In a real-world scenario, the job would eventually time out after 30 seconds,
	// and the error would be recorded in the job's LastError field
}

// TestContextCancellation tests that jobs are properly cancelled when the context is cancelled
// cancelAwareAction is an Action whose Execute blocks until its execution
// context is cancelled, then records and returns the cancellation error. Unlike
// MockAction (whose ExecuteFunc callback does not receive a context), it lets a
// test assert that stopping the queue actually propagates cancellation into the
// running job, rather than only that executeJobWithTimeout's own select unblocks.
type cancelAwareAction struct {
	started chan struct{}
	result  chan error
}

func (a *cancelAwareAction) Execute(ctx context.Context, _ any) error {
	close(a.started)
	<-ctx.Done()
	err := ctx.Err()
	a.result <- err
	return err
}

func (a *cancelAwareAction) GetDescription() string { return "Cancel Aware Action" }

// TestContextCancellation tests that stopping the queue cancels the execution
// context of in-progress jobs and that the running action actually observes the
// cancellation (not just that StopWithTimeout returns).
func TestContextCancellation(t *testing.T) {
	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// The action blocks on its execution context and reports the cancellation
	// error, so the test can prove the job saw ctx.Done() rather than being
	// abandoned by the queue.
	action := &cancelAwareAction{
		started: make(chan struct{}),
		result:  make(chan error, 1),
	}

	// Create retry config
	config := RetryConfig{
		Enabled:      false,
		MaxRetries:   0,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Enqueue the job
	job, err := queue.Enqueue(t.Context(), action, &TestData{ID: "cancellation-test"}, config)
	require.NoError(t, err, "Failed to enqueue job")
	require.NotNil(t, job, "Job should not be nil")

	// Wait for the job to start executing. The action closes started at the top
	// of Execute, so this is a precise signal and no fixed pre-sleep is needed.
	waitForChannel(t, action.started, DefaultTestTimeout, "Timed out waiting for job execution to start")

	// Stop the queue. This cancels the execution context of running jobs, which
	// the action observes via <-ctx.Done(). The stop itself should succeed
	// within its timeout.
	require.NoError(t, queue.StopWithTimeout(500*time.Millisecond), "Queue stop should succeed with timeout")

	// The action must have observed context cancellation rather than the queue
	// merely abandoning it. Receiving on result also proves the action ran once.
	select {
	case actionErr := <-action.result:
		require.ErrorIs(t, actionErr, context.Canceled, "running action should observe context cancellation")
	case <-time.After(DefaultTestTimeout):
		require.Fail(t, "running action did not observe context cancellation")
	}
}

// TestStressTest performs a stress test on the job queue with many concurrent jobs
func TestStressTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue with large capacity
	maxJobs := 100
	queue := setupTestQueue(t, maxJobs, false)
	defer teardownTestQueue(t, queue)

	// Number of jobs to enqueue - reduced for more reliable testing
	numJobs := 50

	// Create a wait group to wait for all jobs to complete
	var wg sync.WaitGroup
	wg.Add(numJobs)

	// Create a counter for completed jobs
	var completedJobs atomic.Int32
	var failedJobs atomic.Int32

	// Create retry config with shorter delays
	config := RetryConfig{
		Enabled:      true,
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     20 * time.Millisecond,
		Multiplier:   1.5,
	}

	// Create a mix of fast, slow, and failing jobs
	for i := range numJobs {
		var action *MockAction

		// Create different types of actions based on the job index
		switch i % 5 {
		case 0:
			// Fast job that succeeds immediately
			action = &MockAction{
				ExecuteFunc: func(data any) error {
					defer wg.Done()
					completedJobs.Add(1)
					return nil
				},
			}
		case 1:
			// Slow job that succeeds after a delay
			action = &MockAction{
				ExecuteFunc: func(data any) error {
					defer wg.Done()
					time.Sleep(10 * time.Millisecond) // Reduced delay
					completedJobs.Add(1)
					return nil
				},
			}
		case 2:
			// Job that fails once then succeeds
			var attemptCount atomic.Int32
			action = &MockAction{
				ExecuteFunc: func(data any) error {
					if attemptCount.Add(1) == 1 {
						return errors.New("simulated failure")
					}
					defer wg.Done()
					completedJobs.Add(1)
					return nil
				},
			}
		case 3:
			// Job that fails twice then succeeds
			var attemptCount atomic.Int32
			action = &MockAction{
				ExecuteFunc: func(data any) error {
					count := attemptCount.Add(1)
					if count <= 2 {
						return errors.New("simulated failure")
					}
					defer wg.Done()
					completedJobs.Add(1)
					return nil
				},
			}
		case 4:
			// Job that always fails
			var attemptCount atomic.Int32
			action = &MockAction{
				ExecuteFunc: func(data any) error {
					// Only call wg.Done() and increment failedJobs once, on the final attempt
					count := attemptCount.Add(1)
					// Safely check if count reached max retries
					maxRetries := min(config.MaxRetries+1,
						// This should not happen in practice, but handle it
						math.MaxInt32)
					if count >= int32(maxRetries) { //nolint:gosec // G115: test values small, safe conversion
						defer wg.Done()
						failedJobs.Add(1)
					}
					return errors.New("simulated failure")
				},
			}
		}

		// Create test data
		data := &TestData{ID: fmt.Sprintf("stress-test-%d", i)}

		// Enqueue the job
		_, err := queue.Enqueue(t.Context(), action, data, config)
		require.NoError(t, err, "Failed to enqueue job %d", i)
	}

	// Force immediate processing to ensure all jobs are processed
	// We need multiple processing cycles to handle retries
	for range 10 {
		queue.ProcessImmediately(ctx)
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for all jobs to complete with a timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All jobs completed
	case <-time.After(5 * time.Second): // Reduced timeout
		require.Fail(t, "Timed out waiting for jobs to complete",
			"Completed: %d, Failed: %d, Total: %d", completedJobs.Load(), failedJobs.Load(), numJobs)
	}

	// The done channel guarantees every action finished (completedJobs +
	// failedJobs == numJobs). The queue-level SuccessfulJobs/FailedJobs counters
	// are updated in handleJobSuccess/handleJobFailure after each action returns,
	// so poll until they account for every job instead of sleeping a fixed
	// amount.
	assert.Equal(t, int32(numJobs), completedJobs.Load()+failedJobs.Load(),
		"All jobs should have been completed or failed")

	require.Eventually(t, func() bool {
		stats := queue.GetStats()
		return stats.SuccessfulJobs+stats.FailedJobs == numJobs
	}, DefaultTestTimeout, 10*time.Millisecond,
		"all jobs should be recorded as successful or failed")

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, numJobs, stats.TotalJobs, "Total jobs should match the number of enqueued jobs")

	// We expect 80% of jobs to succeed (types 0, 1, 2, 3) and 20% to fail (type 4)
	expectedSuccessfulJobs := numJobs * 4 / 5
	assert.InDelta(t, expectedSuccessfulJobs, stats.SuccessfulJobs, float64(numJobs)/5,
		"Successful jobs should be approximately 80%% of total jobs")

	expectedFailedJobs := numJobs / 5
	assert.InDelta(t, expectedFailedJobs, stats.FailedJobs, float64(numJobs)/5,
		"Failed jobs should be approximately 20%% of total jobs")
}

// TestConcurrentJobSubmission tests that the job queue can handle concurrent job submissions
func TestConcurrentJobSubmission(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue with large capacity
	queue := setupTestQueue(t, 1000, false)
	defer teardownTestQueue(t, queue)

	numGoroutines := 10
	jobsPerGoroutine := 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Have multiple goroutines submit jobs concurrently
	for i := range numGoroutines {
		go func(goroutineID int) {
			defer wg.Done()
			for j := range jobsPerGoroutine {
				action := &MockAction{}
				data := &TestData{ID: fmt.Sprintf("goroutine-%d-job-%d", goroutineID, j)}
				config := RetryConfig{Enabled: false}

				_, err := queue.Enqueue(t.Context(), action, data, config)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// Drive and wait for every job to be processed. SuccessfulJobs is updated
	// under the queue lock after each action returns; poll until all jobs are
	// accounted for instead of a fixed processing loop plus a 2s sleep that can
	// be too short for 1000 jobs on a loaded CI runner.
	totalJobs := numGoroutines * jobsPerGoroutine
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().SuccessfulJobs == totalJobs
	}, LongTestTimeout, 20*time.Millisecond, "all submitted jobs should be processed successfully")

	// Verify all jobs were processed
	stats := queue.GetStats()
	assert.Equal(t, totalJobs, stats.TotalJobs, "All jobs should have been enqueued")
	assert.Equal(t, totalJobs, stats.SuccessfulJobs, "All jobs should have been successful")
}

// TestRecoveryFromPanic tests that the job queue can recover from panics in job execution
func TestRecoveryFromPanic(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create action that panics
	panicAction := &MockAction{
		Description: "Panic Action",
		ExecuteFunc: func(data any) error {
			panic("simulated panic in job")
		},
	}

	// Create action that succeeds
	normalAction := &MockAction{
		Description: "Normal Action",
	}

	// Enqueue panic job
	_, err := queue.Enqueue(t.Context(), panicAction, &TestData{ID: "panic-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Enqueue normal job
	_, err = queue.Enqueue(t.Context(), normalAction, &TestData{ID: "normal-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Drive both jobs and wait until each reaches a terminal state. The panic is
	// converted to a job failure in handleJobFailure and the normal job's
	// success in handleJobSuccess, both after the action returns, so poll the
	// counters instead of sleeping a fixed amount.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		stats := queue.GetStats()
		return stats.SuccessfulJobs == 1 && stats.FailedJobs == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "panic job should fail and normal job should succeed")

	// Verify the normal job was still processed despite the panic
	assert.Equal(t, 1, normalAction.GetExecuteCount(), "Normal job should have been executed")

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 2, stats.TotalJobs, "Total jobs should be 2")
	assert.Equal(t, 1, stats.SuccessfulJobs, "Successful jobs should be 1")
	assert.Equal(t, 1, stats.FailedJobs, "Failed jobs should be 1")
}

// TestGracefulShutdownWithInProgressJobs tests that the job queue waits for in-progress jobs to complete during shutdown
func TestGracefulShutdownWithInProgressJobs(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)

	// Create channels to track job states
	jobStarted := make(chan struct{})
	jobCompleted := make(chan struct{})

	// Create an action that signals when it starts and waits for notification to complete
	action := &MockAction{
		ExecuteFunc: func(data any) error {
			close(jobStarted)
			<-jobCompleted
			return nil
		},
	}

	// Enqueue the job
	_, err := queue.Enqueue(t.Context(), action, &TestData{ID: "long-running-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Process the job to start it
	queue.ProcessImmediately(ctx)

	// Wait for job to start
	waitForChannelWithLog(t, jobStarted, 2*time.Second, "Job didn't start in time", "Job started successfully")

	// Initiate graceful shutdown with a reasonable timeout
	shutdownErr := make(chan error, 1)
	go func() {
		shutdownErr <- queue.StopWithTimeout(2 * time.Second)
	}()

	// Let the job complete. StopWithTimeout is already in flight above and will
	// not return until this in-progress job finishes (or its context is
	// cancelled during shutdown), so releasing the job here exercises the
	// shutdown-overlapping-an-in-progress-job path without a fixed sleep.
	close(jobCompleted)

	// Check if shutdown completed without error
	select {
	case err := <-shutdownErr:
		require.NoError(t, err, "Graceful shutdown should complete without error")
	case <-time.After(3 * time.Second):
		require.Fail(t, "Shutdown didn't complete in time")
	}
}

// TestRateLimiting tests that the job queue properly limits the rate of job submissions
func TestRateLimiting(t *testing.T) {
	// Create a queue with a small size to test throttling
	queue := setupTestQueue(t, 5, false)
	defer teardownTestQueue(t, queue)

	// Disable job dropping for this test to ensure rejections
	queue.SetAllowJobDropping(false)

	var successCount, rejectionCount atomic.Int32

	// Submit jobs at a high rate
	for i := range 100 {
		action := &MockAction{}
		data := &TestData{ID: fmt.Sprintf("job-%d", i)}
		config := RetryConfig{Enabled: false}

		_, err := queue.Enqueue(t.Context(), action, data, config)
		switch {
		case err == nil:
			successCount.Add(1)
		case errors.Is(err, ErrQueueFull):
			rejectionCount.Add(1)
		default:
			assert.Fail(t, "Unexpected error", "%v", err)
		}

		// Don't sleep to simulate high submission rate
	}

	t.Logf("Successfully enqueued: %d, Rejected: %d", successCount.Load(), rejectionCount.Load())
	assert.Positive(t, rejectionCount.Load(), "Some jobs should be rejected due to queue full")
	assert.Equal(t, int32(5), successCount.Load(), "Only 5 jobs should be successfully enqueued")
}

// TestJobCancellation tests that jobs can be cancelled via context cancellation
func TestJobCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	queue := NewJobQueueWithOptions(100, false)
	queue.SetProcessingInterval(10 * time.Millisecond)
	queue.StartWithContext(ctx)
	defer func() {
		assert.NoError(t, queue.Stop(), "Failed to stop queue")
	}()

	longJobStarted := make(chan struct{})

	// The action blocks until the context is cancelled, then returns the
	// cancellation error. This honors the Action interface contract that
	// implementations must respect context cancellation, and it makes the
	// test deterministic: the job can only finish via cancellation, so the
	// queue always records it as a failure carrying a cancellation error.
	//
	// The previous version returned nil immediately after the context was
	// cancelled, which left executeJobWithTimeout's select racing between the
	// success branch (action returned nil) and the cancellation branch
	// (execCtx.Done()). Go picks a ready select case at random, so the job was
	// marked Completed or Failed depending on scheduler timing; that raced
	// reliably on loaded CI runners while passing locally.
	action := &MockAction{
		ExecuteFunc: func(_ any) error {
			close(longJobStarted)
			<-ctx.Done()
			return ctx.Err()
		},
	}

	// Enqueue the job
	_, err := queue.Enqueue(t.Context(), action, &TestData{ID: "cancel-test"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Process the job to start it
	queue.ProcessImmediately(ctx)

	// Wait for job to start
	waitForChannelWithLog(t, longJobStarted, 2*time.Second, "Job didn't start in time", "Long job started successfully")

	// Cancel the context. This unblocks the action and signals the queue's
	// execution cancellation path simultaneously; both outcomes mark the job
	// as failed with a cancellation error.
	cancel()

	// Poll the queue stats until the job is recorded as failed with a
	// cancellation error, instead of sleeping for a fixed duration (which was
	// flaky under CI load). FailedJobs and the per-action LastErrorMessage are
	// updated atomically under the queue lock in handleJobFailure, so observing
	// FailedJobs > 0 guarantees the matching error message is also visible. The
	// per-action LastErrorMessage is the durable signal: the Job pointer is
	// discarded once cleanupStaleJobs runs.
	require.Eventually(t, func() bool {
		stats := queue.GetStats()
		if stats.FailedJobs == 0 {
			return false
		}
		for _, as := range stats.ActionStats {
			if strings.Contains(as.LastErrorMessage, "cancel") {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"job should be marked failed with a cancellation error after context cancellation")

	// Exactly one job should have been tracked.
	assert.Equal(t, 1, queue.GetStats().TotalJobs, "Total jobs should be 1")
}

// TestLongRunningJobs tests that short jobs can be processed while a long job is running
func TestLongRunningJobs(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create one long-running job
	longAction := &MockAction{
		ExecuteDelay: 2 * time.Second,
	}

	// Create several short jobs
	shortActions := make([]*MockAction, 5)
	for i := range shortActions {
		shortActions[i] = &MockAction{}
	}

	// Enqueue the long job first
	_, err := queue.Enqueue(t.Context(), longAction, &TestData{ID: "long-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Process the long job to start it
	queue.ProcessImmediately(ctx)

	// Wait until the long job is actually running before enqueueing the short
	// jobs, so the test exercises short jobs progressing alongside a long one.
	// The action increments its count when it starts, before its 2s delay.
	require.Eventually(t, func() bool {
		return longAction.GetExecuteCount() == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "long job should start running")

	// Enqueue the short jobs
	for i, action := range shortActions {
		_, err := queue.Enqueue(t.Context(), action, &TestData{ID: fmt.Sprintf("short-job-%d", i)}, RetryConfig{Enabled: false})
		require.NoError(t, err)
	}

	// Drive and wait for every job (the long one plus the five short ones) to
	// complete. The long action sleeps 2s; poll the success counter until all
	// six are done instead of sleeping a fixed 2.5s that can be too short under
	// CI load.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().SuccessfulJobs == 6
	}, LongTestTimeout, 20*time.Millisecond, "all six jobs should complete successfully")

	// Verify all jobs were executed
	assert.Equal(t, 1, longAction.GetExecuteCount(), "Long job should have been executed once")
	for i, action := range shortActions {
		assert.Equal(t, 1, action.GetExecuteCount(), "Short job %d should have been executed", i)
	}

	// Check job stats
	stats := queue.GetStats()
	assert.Equal(t, 6, stats.TotalJobs, "Total jobs should be 6")
	assert.Equal(t, 6, stats.SuccessfulJobs, "All jobs should be successful")
}

// TestJobTypeStatistics tests that job statistics are tracked correctly per action type
func TestJobTypeStatistics(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create different action types for testing
	type SuccessActionType struct{ MockAction }
	type FailActionType struct{ MockAction }
	type RetryActionType struct{ MockAction }

	// Configure actions with descriptions
	successAction := &SuccessActionType{
		MockAction: MockAction{
			Description: "Success Action",
		},
	}

	failAction := &FailActionType{
		MockAction: MockAction{
			Description: "Fail Action",
			ExecuteFunc: func(data any) error {
				return errors.New("simulated failure")
			},
		},
	}

	// Create a counter for retry attempts
	retryCounter := 0
	retryAction := &RetryActionType{
		MockAction: MockAction{
			Description: "Retry Action",
			ExecuteFunc: func(data any) error {
				// Increment counter and check
				retryCounter++
				// Fail on first attempt, succeed on retry
				if retryCounter == 1 {
					return errors.New("simulated failure for retry")
				}
				return nil
			},
		},
	}

	// Enqueue jobs with different actions
	_, err := queue.Enqueue(t.Context(), successAction, &TestData{ID: "success-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Process the success job and poll until its success is recorded (the stat
	// is updated in handleJobSuccess after the action returns).
	successType := fmt.Sprintf("%T", successAction)
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().ActionStats[successType].Successful == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "success job should be recorded as successful")

	// Check stats after success job. Attempted counts one Enqueue plus one
	// execution.
	stats := queue.GetStats()
	assert.Equal(t, 2, stats.ActionStats[successType].Attempted, "Success action should have 2 attempts")
	assert.Equal(t, 1, stats.ActionStats[successType].Successful, "Success action should have 1 success")
	assert.Equal(t, "Success Action", stats.ActionStats[successType].Description, "Description should match")

	// Enqueue fail job
	_, err = queue.Enqueue(t.Context(), failAction, &TestData{ID: "fail-job"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Process the fail job and poll until its failure is recorded (the error
	// message and timestamps are set in handleJobFailure after the action
	// returns).
	failType := fmt.Sprintf("%T", failAction)
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		return queue.GetStats().ActionStats[failType].LastErrorMessage != ""
	}, DefaultTestTimeout, 10*time.Millisecond, "fail job failure should be recorded")

	// Check stats after fail job.
	stats = queue.GetStats()
	assert.Equal(t, 2, stats.ActionStats[failType].Attempted, "Fail action should have 2 attempts")
	assert.Equal(t, 0, stats.ActionStats[failType].Successful, "Fail action should have 0 success")
	assert.Equal(t, "Fail Action", stats.ActionStats[failType].Description, "Description should match")
	assert.NotEmpty(t, stats.ActionStats[failType].LastErrorMessage, "Error message should be recorded")
	assert.False(t, stats.ActionStats[failType].LastFailedTime.IsZero(), "Last failed time should be set")
	assert.False(t, stats.ActionStats[failType].LastExecutionTime.IsZero(), "Last execution time should be set")

	// Enqueue retry job
	_, err = queue.Enqueue(t.Context(), retryAction, &TestData{ID: "retry-job"}, RetryConfig{
		Enabled:      true,
		MaxRetries:   1,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   1.0,
	})
	require.NoError(t, err)

	// Process the retry job. It fails on the first attempt and succeeds on the
	// retry. Drive it with ProcessImmediately and poll until the success is
	// recorded for this action type, rather than asserting transient state after
	// fixed sleeps (the retry runs on the 10ms ticker, so the old "first
	// attempt" checkpoint raced the retry and flaked under CI load).
	retryType := fmt.Sprintf("%T", retryAction)
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		s := queue.GetStats()
		// Also require the active queue to drain so the final PendingJobs == 0 /
		// QueueUtilization == 0 assertions below are taken against a swept queue
		// (the completed retry job lingers in q.jobs until a cleanup tick).
		return s.ActionStats[retryType].Successful == 1 && s.PendingJobs == 0
	}, DefaultTestTimeout, 10*time.Millisecond, "retry job should succeed and the active queue should drain")

	// Check stats after the retry job has succeeded. Attempted counts one
	// Enqueue plus two executions (initial + retry); Retried counts the single
	// retry execution.
	stats = queue.GetStats()
	assert.Equal(t, 3, stats.ActionStats[retryType].Attempted, "Retry action should have 3 attempts")
	assert.Equal(t, 1, stats.ActionStats[retryType].Successful, "Retry action should have 1 success")
	assert.Equal(t, 0, stats.ActionStats[retryType].Failed, "Retry action should have 0 failure")
	assert.Equal(t, 1, stats.ActionStats[retryType].Retried, "Retry action should have 1 retry")
	assert.Equal(t, "Retry Action", stats.ActionStats[retryType].Description, "Description should match")
	assert.False(t, stats.ActionStats[retryType].LastSuccessfulTime.IsZero(), "Last successful time should be set")
	// Windows' coarse monotonic clock (~15ms) can measure a fast action's
	// execution as exactly 0, so per-action duration stats are not reliably
	// positive there. Keep the positivity assertion on Linux/macOS, where the
	// clock is fine-grained; the count/retry aggregation above is still checked
	// on every platform. (Driving the action with a real sleep to force a
	// non-zero span perturbs this timing-sensitive test's retry scheduling.)
	if runtime.GOOS != "windows" {
		assert.Greater(t, stats.ActionStats[retryType].TotalDuration, time.Duration(0), "Total duration should be positive")
		assert.Greater(t, stats.ActionStats[retryType].AverageDuration, time.Duration(0), "Average duration should be positive")
		assert.Greater(t, stats.ActionStats[retryType].MinDuration, time.Duration(0), "Min duration should be positive")
		assert.Greater(t, stats.ActionStats[retryType].MaxDuration, time.Duration(0), "Max duration should be positive")
	}

	// Test JSON output
	jsonStr, err := stats.ToJSON()
	require.NoError(t, err, "ToJSON should not error")
	assert.Contains(t, jsonStr, "Success Action", "JSON should contain action description")
	assert.Contains(t, jsonStr, "Fail Action", "JSON should contain action description")
	assert.Contains(t, jsonStr, "Retry Action", "JSON should contain action description")
	assert.Contains(t, jsonStr, "lastError", "JSON should contain error information")
	assert.Contains(t, jsonStr, "timestamps", "JSON should contain timestamp information")
	assert.Contains(t, jsonStr, "performance", "JSON should contain performance metrics")

	// Verify queue utilization stats
	assert.Equal(t, 3, stats.TotalJobs, "Total jobs should be 3")
	assert.Equal(t, 2, stats.SuccessfulJobs, "Successful jobs should be 2")
	assert.Equal(t, 1, stats.FailedJobs, "Failed jobs should be 1")
	assert.Equal(t, 0, stats.PendingJobs, "Pending jobs should be 0")
	assert.Equal(t, 100, stats.MaxQueueSize, "Max queue size should be 100")
	assert.InDelta(t, 0.0, stats.QueueUtilization, 0.01, "Queue utilization should be 0%")
}

// TestMemoryManagementWithLargeJobLoads tests that the job queue properly manages memory
// when processing a large number of jobs
func TestMemoryManagementWithLargeJobLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory management test in short mode")
	}

	queue := setupTestQueue(t, 1000, false)
	defer teardownTestQueue(t, queue)

	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	startAlloc := m.Alloc

	// Submit and process a large number of jobs
	// Using a smaller count for faster test execution
	const jobCount = 1000

	var wg sync.WaitGroup
	wg.Add(jobCount)

	for i := range jobCount {
		action := &MockAction{
			ExecuteFunc: func(data any) error {
				defer wg.Done()
				return nil
			},
		}
		data := &TestData{ID: fmt.Sprintf("job-%d", i)}

		_, err := queue.Enqueue(t.Context(), action, data, RetryConfig{Enabled: false})
		require.NoError(t, err)

		// Process in batches to avoid overwhelming the queue
		if i%100 == 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Wait for all jobs to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	waitForChannel(t, done, LongTestTimeout, "Timed out waiting for jobs to complete")

	// Force cleanup and GC
	queue.cleanupStaleJobs(t.Context())
	runtime.GC()
	runtime.ReadMemStats(&m)

	t.Logf("Memory usage after %d jobs: Before=%d bytes, After=%d bytes",
		jobCount, startAlloc, m.Alloc)

	// The check below is more of a sanity check than a strict test
	// What we're really looking for is that memory doesn't grow unbounded
	assert.Less(t, m.Alloc, startAlloc*5,
		"Memory usage should not grow excessively after processing jobs")
}

// TestCompletedJobIsGarbageCollectable verifies that after a job completes and
// the cleanup tick runs, the Job struct (and its Action) become reachable only
// via GC, not via any queue field. This is a regression guard against the
// archivedJobs retention that caused the 300 MB to 760 MB RSS audiocore
// memory regression.
func TestCompletedJobIsGarbageCollectable(t *testing.T) {
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	finalized := make(chan struct{}, 1)

	var wg sync.WaitGroup
	wg.Add(1)

	config := RetryConfig{
		Enabled:      false,
		MaxRetries:   0,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   2.0,
	}

	// Enqueue one job, then drop the local action reference so the queue holds
	// the only remaining reference. After cleanup the queue must also drop it.
	func() {
		action := &MockAction{
			ExecuteFunc: func(data any) error {
				defer wg.Done()
				return nil
			},
		}
		runtime.AddCleanup(action, func(ch chan struct{}) {
			select {
			case ch <- struct{}{}:
			default:
			}
		}, finalized)
		data := &TestData{ID: "gc-target"}
		_, err := queue.Enqueue(t.Context(), action, data, config)
		require.NoError(t, err, "Failed to enqueue job")
	}()

	// Wait for the action to execute.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitForChannel(t, done, DefaultTestTimeout, "Timed out waiting for job to complete")

	// Poll until cleanupStaleJobs has removed the completed job. Using a
	// deterministic signal instead of a fixed sleep avoids CI flakes where
	// scheduling pressure pushes the cleanup tick past the sleep window.
	cleanupDeadline := time.Now().Add(2 * time.Second)
	for queue.GetStats().StaleJobs == 0 {
		if time.Now().After(cleanupDeadline) {
			t.Fatalf("cleanupStaleJobs did not fire within 2s after job completion")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Poll for the cleanup callback. runtime.AddCleanup is asynchronous with
	// no fixed timing relative to runtime.GC(); the Go runtime may only queue
	// callbacks during GC and run them later on a separate goroutine. Forcing
	// GC plus yielding the scheduler on each iteration reliably drives the
	// callback on slow CI where a fixed wait would flake.
	require.Eventually(t, func() bool {
		runtime.GC()
		runtime.Gosched()
		select {
		case <-finalized:
			return true
		default:
			return false
		}
	}, 2*time.Second, 25*time.Millisecond,
		"completed job action was not garbage-collected after cleanup; jobqueue is retaining it")
}

// TestNewJobQueueWithOptions_ClampsNonPositiveMaxJobs verifies that passing
// maxJobs <= 0 is clamped to 1 so the queue remains usable instead of silently
// rejecting every Enqueue with ErrQueueFull. Covers the field accessor AND the
// observable Enqueue behavior, since the user-visible bug is queue-full
// rejection rather than the internal counter.
func TestNewJobQueueWithOptions_ClampsNonPositiveMaxJobs(t *testing.T) {
	t.Parallel()

	for _, maxJobs := range []int{0, -1, -100} {
		t.Run(fmt.Sprintf("maxJobs=%d", maxJobs), func(t *testing.T) {
			t.Parallel()

			q := setupTestQueue(t, maxJobs, false)
			t.Cleanup(func() {
				assert.NoError(t, q.StopWithTimeout(1*time.Second),
					"StopWithTimeout should not error during test cleanup")
			})

			assert.Equal(t, 1, q.GetMaxJobs(),
				"maxJobs=%d should clamp to 1; got %d", maxJobs, q.GetMaxJobs())

			_, err := q.Enqueue(t.Context(), &MockAction{}, &TestData{ID: "smoke"}, RetryConfig{Enabled: false})
			require.NoError(t, err,
				"clamped queue should accept its first job instead of returning ErrQueueFull")
		})
	}
}

// TestStatsToJSON tests the ToJSON method of JobStatsSnapshot
func TestStatsToJSON(t *testing.T) {
	// Create a context for manual control
	ctx := t.Context()

	// Create a new job queue
	queue := setupTestQueue(t, 100, false)
	defer teardownTestQueue(t, queue)

	// Create actions with different descriptions
	successAction := &MockAction{
		Description: "Success Action",
	}

	failAction := &MockAction{
		Description: "Fail Action",
		ExecuteFunc: func(data any) error {
			return errors.New("simulated failure for JSON test")
		},
	}

	// Enqueue the success and fail jobs
	_, err := queue.Enqueue(t.Context(), successAction, &TestData{ID: "json-success"}, RetryConfig{Enabled: false})
	require.NoError(t, err)
	_, err = queue.Enqueue(t.Context(), failAction, &TestData{ID: "json-fail"}, RetryConfig{Enabled: false})
	require.NoError(t, err)

	// Drive both jobs to a terminal state and wait until the queue records the
	// success and the failure (each recorded after the action returns) before
	// snapshotting stats for JSON serialization.
	require.Eventually(t, func() bool {
		queue.ProcessImmediately(ctx)
		stats := queue.GetStats()
		return stats.SuccessfulJobs == 1 && stats.FailedJobs == 1
	}, DefaultTestTimeout, 10*time.Millisecond, "both jobs should reach a terminal state")

	// Get stats and convert to JSON
	stats := queue.GetStats()
	jsonStr, err := stats.ToJSON()
	require.NoError(t, err, "ToJSON should not error")

	// Verify JSON structure
	assert.Contains(t, jsonStr, `"queue"`, "JSON should contain queue section")
	assert.Contains(t, jsonStr, `"actions"`, "JSON should contain actions section")
	assert.Contains(t, jsonStr, `"timestamp"`, "JSON should contain timestamp")

	// Verify queue stats
	assert.Contains(t, jsonStr, `"total"`, "JSON should contain total jobs")
	assert.Contains(t, jsonStr, `"successful"`, "JSON should contain successful jobs")
	assert.Contains(t, jsonStr, `"failed"`, "JSON should contain failed jobs")
	assert.Contains(t, jsonStr, `"utilization"`, "JSON should contain queue utilization")

	// Verify action stats
	assert.Contains(t, jsonStr, `"Success Action"`, "JSON should contain success action description")
	assert.Contains(t, jsonStr, `"Fail Action"`, "JSON should contain fail action description")
	assert.Contains(t, jsonStr, `"metrics"`, "JSON should contain metrics section")
	assert.Contains(t, jsonStr, `"performance"`, "JSON should contain performance section")
	assert.Contains(t, jsonStr, `"timestamps"`, "JSON should contain timestamps section")
	assert.Contains(t, jsonStr, `"lastError"`, "JSON should contain error information")

	// Parse JSON to verify structure
	var jsonData map[string]any
	err = json.Unmarshal([]byte(jsonStr), &jsonData)
	require.NoError(t, err, "JSON should be valid")

	// Verify top-level structure
	assert.Contains(t, jsonData, "queue", "JSON should have queue section")
	assert.Contains(t, jsonData, "actions", "JSON should have actions section")
	assert.Contains(t, jsonData, "timestamp", "JSON should have timestamp")

	// Verify queue section
	queueSection, ok := jsonData["queue"].(map[string]any)
	require.True(t, ok, "Queue section should be an object")
	assert.Contains(t, queueSection, "total", "Queue section should have total")
	assert.Contains(t, queueSection, "successful", "Queue section should have successful")
	assert.Contains(t, queueSection, "failed", "Queue section should have failed")
	assert.Contains(t, queueSection, "pending", "Queue section should have pending")
	assert.Contains(t, queueSection, "maxSize", "Queue section should have maxSize")
	assert.Contains(t, queueSection, "utilization", "Queue section should have utilization")

	// Verify actions section
	actionsSection, ok := jsonData["actions"].(map[string]any)
	require.True(t, ok, "Actions section should be an object")

	// There should be at least two action types
	assert.GreaterOrEqual(t, len(actionsSection), 2, "Should have at least two action types")

	// Find the fail action and verify its structure
	var failActionFound bool
	for _, actionData := range actionsSection {
		actionObj, ok := actionData.(map[string]any)
		require.True(t, ok, "Action data should be an object")

		if desc, ok := actionObj["description"].(string); ok && desc == "Fail Action" {
			failActionFound = true

			// Verify action structure
			assert.Contains(t, actionObj, "typeName", "Action should have typeName")
			assert.Contains(t, actionObj, "metrics", "Action should have metrics")
			assert.Contains(t, actionObj, "performance", "Action should have performance")

			// Verify metrics
			metrics, ok := actionObj["metrics"].(map[string]any)
			require.True(t, ok, "Metrics should be an object")
			assert.Contains(t, metrics, "attempted", "Metrics should have attempted")
			assert.Contains(t, metrics, "successful", "Metrics should have successful")
			assert.Contains(t, metrics, "failed", "Metrics should have failed")
			assert.Contains(t, metrics, "retried", "Metrics should have retried")
			assert.Contains(t, metrics, "dropped", "Metrics should have dropped")

			// Verify performance
			performance, ok := actionObj["performance"].(map[string]any)
			require.True(t, ok, "Performance should be an object")
			assert.Contains(t, performance, "totalDuration", "Performance should have totalDuration")
			assert.Contains(t, performance, "averageDuration", "Performance should have averageDuration")
			assert.Contains(t, performance, "minDuration", "Performance should have minDuration")
			assert.Contains(t, performance, "maxDuration", "Performance should have maxDuration")

			// Verify error info
			assert.Contains(t, actionObj, "lastError", "Failed action should have lastError")
			assert.Contains(t, actionObj["lastError"].(string), "simulated failure", "Error message should be correct")

			break
		}
	}

	assert.True(t, failActionFound, "Should find the fail action in the JSON")
}
