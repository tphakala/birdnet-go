// Package jobqueue provides structured logging for the jobqueue package
package jobqueue

import (
	"log/slog"
	"time"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for job queue operations
var logger *slog.Logger

func init() {
	// Create service-specific logger for analysis job queue
	// This provides dedicated logging for job queue operations
	logger = logging.ForService("analysis.jobqueue")
	
	// Defensive initialization for early startup scenarios
	// This ensures we always have a working logger even if
	// the logging system isn't fully initialized yet
	if logger == nil {
		logger = slog.Default().With("service", "analysis.jobqueue")
	}
}

// GetLogger returns the jobqueue package logger
// Useful for external packages that need access to jobqueue logging
func GetLogger() *slog.Logger {
	if logger == nil {
		// Double-check initialization in case of race conditions
		logger = logging.ForService("analysis.jobqueue")
		if logger == nil {
			logger = slog.Default().With("service", "analysis.jobqueue")
		}
	}
	return logger
}

// LogJobEnqueued logs when a job is successfully enqueued
func LogJobEnqueued(jobID, actionType string, priority int) {
	logger.Debug("Job enqueued",
		"job_id", jobID,
		"action_type", actionType,
		"priority", priority)
}

// LogJobStarted logs when a job starts processing
func LogJobStarted(jobID, actionType string, attempt int) {
	logger.Debug("Job started",
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt)
}

// LogJobCompleted logs when a job completes successfully
func LogJobCompleted(jobID, actionType string, duration time.Duration) {
	logger.Info("Job completed",
		"job_id", jobID,
		"action_type", actionType,
		"duration_ms", duration.Milliseconds())
}

// LogJobFailed logs when a job fails
func LogJobFailed(jobID, actionType string, attempt, maxRetries int, err error) {
	logger.Warn("Job failed",
		"job_id", jobID,
		"action_type", actionType,
		"attempt", attempt,
		"max_retries", maxRetries,
		"error", err,
		"will_retry", attempt < maxRetries)
}

// LogQueueStats logs periodic queue statistics
func LogQueueStats(pending, running, completed, failed int) {
	logger.Info("Queue statistics",
		"pending", pending,
		"running", running,
		"completed", completed,
		"failed", failed,
		"total", pending+running+completed+failed)
}