// jobqueue_adapter.go provides adapter functions for using the jobqueue package
package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
)

// RetryConfig holds the configuration for retry behavior of an action
// DEPRECATED: Use jobqueue.RetryConfig directly instead.
// This is kept for backward compatibility with existing code.
type RetryConfig struct {
	Enabled      bool          // Whether retry is enabled for this action
	MaxRetries   int           // Maximum number of retry attempts
	InitialDelay time.Duration // Initial delay before first retry
	MaxDelay     time.Duration // Maximum delay between retries
	Multiplier   float64       // Backoff multiplier for each subsequent retry
}

// ActionAdapter adapts the processor.Action interface to the jobqueue.Action interface
type ActionAdapter struct {
	action Action
}

// Execute implements the jobqueue.Action interface
func (a *ActionAdapter) Execute(data interface{}) error {
	return a.action.Execute(data)
}

// convertRetryConfig converts a processor.RetryConfig to a jobqueue.RetryConfig
// DEPRECATED: Use jobqueue.RetryConfig directly instead.
func convertRetryConfig(config RetryConfig) jobqueue.RetryConfig {
	return jobqueue.RetryConfig{
		Enabled:      config.Enabled,
		MaxRetries:   config.MaxRetries,
		InitialDelay: config.InitialDelay,
		MaxDelay:     config.MaxDelay,
		Multiplier:   config.Multiplier,
	}
}

// convertFromJobQueueRetryConfig converts a jobqueue.RetryConfig to a processor.RetryConfig
// DEPRECATED: Use jobqueue.RetryConfig directly instead.
func convertFromJobQueueRetryConfig(config jobqueue.RetryConfig) RetryConfig {
	return RetryConfig{
		Enabled:      config.Enabled,
		MaxRetries:   config.MaxRetries,
		InitialDelay: config.InitialDelay,
		MaxDelay:     config.MaxDelay,
		Multiplier:   config.Multiplier,
	}
}

// InitProcessor sets the global processor instance for EnqueueTask
func InitProcessor(processor *Processor) {
	p = processor
}

// Global processor instance for EnqueueTask
var p *Processor

// JobQueue is kept for backward compatibility
type JobQueue = jobqueue.JobQueue

// JobStatsSnapshot is kept for backward compatibility
type JobStatsSnapshot = jobqueue.JobStatsSnapshot

// NewJobQueue creates a new job queue with default settings
func NewJobQueue() *jobqueue.JobQueue {
	return jobqueue.NewJobQueue()
}

// GetDefaultRetryConfig returns a default retry configuration
// This is a wrapper around jobqueue.GetDefaultRetryConfig for backward compatibility
func GetDefaultRetryConfig(enabled bool) RetryConfig {
	jqConfig := jobqueue.GetDefaultRetryConfig(enabled)
	return convertFromJobQueueRetryConfig(jqConfig)
}
