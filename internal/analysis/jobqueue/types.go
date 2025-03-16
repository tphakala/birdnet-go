// Package jobqueue provides a job queue implementation with retry capabilities
// for handling asynchronous tasks with configurable retry policies.
package jobqueue

import (
	"errors"
	"time"
)

// Constants for message length limits
const (
	// MaxMessageLength is the maximum length for error messages and descriptions
	// to prevent memory bloat in logs and JSON output
	MaxMessageLength = 500
)

// Common errors that can be returned by job queue operations
var (
	ErrNilAction     = errors.New("cannot enqueue nil action")
	ErrQueueStopped  = errors.New("job queue has been stopped")
	ErrJobNotFound   = errors.New("job not found in queue")
	ErrInvalidStatus = errors.New("invalid job status")
	ErrQueueFull     = errors.New("job queue is full")
)

// RetryConfig holds the configuration for retry behavior of an action
type RetryConfig struct {
	Enabled      bool          // Whether retry is enabled for this action
	MaxRetries   int           // Maximum number of retry attempts
	InitialDelay time.Duration // Initial delay before first retry
	MaxDelay     time.Duration // Maximum delay between retries
	Multiplier   float64       // Backoff multiplier for each subsequent retry
}

// Action defines the interface that must be implemented by any action
// that can be executed by the job queue.
type Action interface {
	Execute(data interface{}) error
	GetDescription() string // Returns a human-readable description of the action
}

// Clock is an interface for time-related operations that can be mocked for testing
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

// RealClock is the default implementation of Clock that uses the actual system clock
type RealClock struct{}

// Now returns the current time
func (c *RealClock) Now() time.Time {
	return time.Now()
}

// Sleep pauses the current goroutine for the specified duration
func (c *RealClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

// After returns a channel that will receive the current time after the specified duration
func (c *RealClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// JobStatus represents the current status of a job in the queue
type JobStatus int

const (
	// JobStatusPending indicates the job is waiting to be executed
	JobStatusPending JobStatus = iota
	// JobStatusRunning indicates the job is currently being executed
	JobStatusRunning
	// JobStatusCompleted indicates the job has completed successfully
	JobStatusCompleted
	// JobStatusFailed indicates the job has failed and will not be retried
	JobStatusFailed
	// JobStatusRetrying indicates the job has failed but will be retried
	JobStatusRetrying
	// JobStatusCancelled indicates the job was cancelled before completion
	JobStatusCancelled
)

// String returns a string representation of the job status
func (s JobStatus) String() string {
	switch s {
	case JobStatusPending:
		return "Pending"
	case JobStatusRunning:
		return "Running"
	case JobStatusCompleted:
		return "Completed"
	case JobStatusFailed:
		return "Failed"
	case JobStatusRetrying:
		return "Retrying"
	case JobStatusCancelled:
		return "Cancelled"
	default:
		return "Unknown"
	}
}

// AllowJobDropping controls whether dropping jobs is allowed in tests
var AllowJobDropping = true
