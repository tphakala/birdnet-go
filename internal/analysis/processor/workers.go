// workers.go contains task processing logic for the processor.
package processor

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TaskType defines types of tasks that can be handled by the worker.
type TaskType int

const (
	TaskTypeAction TaskType = iota // Represents an action task type
)

// Sentinel errors for processor operations
var (
	ErrNilTask = errors.Newf("cannot enqueue nil task").
		Component("analysis.processor").
		Category(errors.CategoryValidation).
		Build()
	
	ErrNilAction = errors.Newf("cannot enqueue task with nil action").
		Component("analysis.processor").
		Category(errors.CategoryValidation).
		Build()
)

// Task represents a unit of work, encapsulating the detection and the action to be performed.
type Task struct {
	Type      TaskType
	Detection Detections
	Action    Action
}

// Variable used for testing to override retry configuration
var testRetryConfigOverride func(action Action) (jobqueue.RetryConfig, bool)

// startWorkerPool initializes the job queue for task processing.
// This is kept for backward compatibility but now simply ensures the job queue is started.
func (p *Processor) startWorkerPool() {
	// Create a cancellable context for the job queue
	ctx, cancel := context.WithCancel(context.Background())

	// Store the cancel function in the processor for clean shutdown
	p.workerCancel = cancel

	// Ensure the job queue is started with our context
	p.JobQueue.StartWithContext(ctx)

	logger := GetLogger()
	logger.Info("Job queue started", 
		"max_capacity", p.JobQueue.GetMaxJobs(),
		"component", "analysis.processor",
		"operation", "job_queue_start",
	)
}

// getJobQueueRetryConfig extracts the retry configuration from an action
func getJobQueueRetryConfig(action Action) jobqueue.RetryConfig {
	// Check if we have a test override (defined in workers_test.go)
	if testRetryConfigOverride != nil {
		if config, ok := testRetryConfigOverride(action); ok {
			return config
		}
	}

	switch a := action.(type) {
	case *BirdWeatherAction:
		return a.RetryConfig // Now directly returns jobqueue.RetryConfig
	case *MqttAction:
		return a.RetryConfig // Now directly returns jobqueue.RetryConfig
	default:
		// Default no retry for actions that don't support it
		return jobqueue.RetryConfig{Enabled: false}
	}
}

// sanitizeString applies sanitization rules to remove sensitive information from strings
func sanitizeString(input string) string {
	// Sanitize RTSP URLs with credentials
	rtspRegex := regexp.MustCompile(`(?i)rtsp://[^:]+:[^@]+@`)
	result := rtspRegex.ReplaceAllString(input, "rtsp://[redacted]@")

	// Sanitize MQTT credentials
	mqttRegex := regexp.MustCompile(`(?i)mqtt://[^:]+:[^@]+@`)
	result = mqttRegex.ReplaceAllString(result, "mqtt://[redacted]@")

	// Sanitize API keys - made case-insensitive with (?i)
	apiKeyRegex := regexp.MustCompile(`(?i)(api[_-]?key|apikey|token|secret)[=:]["']?[a-zA-Z0-9_\-\.]{5,}["']?`)
	result = apiKeyRegex.ReplaceAllString(result, "$1=[REDACTED]")

	// Sanitize passwords - expanded to include more variations and made case-insensitive
	passwordRegex := regexp.MustCompile(`(?i)(password|passwd|pwd)[=:]["']?[^&"'\s]+["']?`)
	result = passwordRegex.ReplaceAllString(result, "$1=[REDACTED]")

	// Sanitize OAuth tokens - made case-insensitive
	oauthRegex := regexp.MustCompile(`(?i)(Bearer|OAuth|oauth_token|access_token)[\s=:]["']?[^&"'\s]+["']?`)
	result = oauthRegex.ReplaceAllString(result, "$1 [REDACTED]")

	// Sanitize other potential sensitive information - made case-insensitive
	otherSensitiveRegex := regexp.MustCompile(`(?i)(private|sensitive|credential|auth)[=:]["']?[^&"'\s]+["']?`)
	result = otherSensitiveRegex.ReplaceAllString(result, "$1=[REDACTED]")

	return result
}

// SanitizedError is a custom error type that wraps the original error while sanitizing its message
type SanitizedError struct {
	original     error
	sanitizedMsg string
}

// Error returns the sanitized error message
func (e *SanitizedError) Error() string {
	return e.sanitizedMsg
}

// Unwrap returns the original error, allowing errors.Is() and errors.As() to work with the sanitized error
func (e *SanitizedError) Unwrap() error {
	return e.original
}

// sanitizeError removes sensitive information from error messages
func sanitizeError(err error) error {
	if err == nil {
		return nil
	}

	// Return a new SanitizedError that wraps the original error
	return &SanitizedError{
		original:     err,
		sanitizedMsg: sanitizeString(err.Error()),
	}
}

// sanitizeActionType removes sensitive information from action type strings
func sanitizeActionType(actionType string) string {
	return sanitizeString(actionType)
}

// EnqueueTask adds a task directly to the job queue for processing.
func (p *Processor) EnqueueTask(task *Task) error {
	if task == nil {
		return ErrNilTask
	}

	// Validate the task
	if task.Action == nil {
		return ErrNilAction
	}

	// Cache logger for this function to avoid repeated GetLogger() calls
	logger := GetLogger()

	// Get action description for logging
	actionDesc := task.Action.GetDescription()
	// Sanitize description for error messages (in case it contains sensitive info)
	sanitizedDesc := sanitizeString(actionDesc)

	// Get retry configuration for the action directly as jobqueue.RetryConfig
	jqRetryConfig := getJobQueueRetryConfig(task.Action)

	// Log detailed information about the task being enqueued
	if p.Settings.Debug {
		// Get species name for more informative logging if available
		speciesName := "unknown"
		if task.Detection.Note.CommonName != "" {
			speciesName = task.Detection.Note.CommonName
		}

		// Log the action description and species to provide more context
		logger.Debug("Enqueuing task", 
			"task_description", sanitizedDesc,
			"species", speciesName,
			"retry_enabled", jqRetryConfig.Enabled,
			"max_retries", jqRetryConfig.MaxRetries)
	}

	// Enqueue the task directly to the job queue
	// TODO: Pass proper context when EnqueueTask signature is updated to accept context
	ctx := context.Background()
	job, err := p.JobQueue.Enqueue(ctx, &ActionAdapter{action: task.Action}, task.Detection, jqRetryConfig)
	if err != nil {
		// Handle specific error types with appropriate messages
		switch {
		case strings.Contains(err.Error(), "queue is full"):
			queueSize := p.JobQueue.GetMaxJobs()
			// Log with action description for better context
			logger.Warn("Job queue is full, dropping task", 
				"queue_capacity", queueSize,
				"task_description", sanitizedDesc)

			// Suggest increasing queue size if this happens frequently
			return fmt.Errorf("job queue is full (capacity: %d), dropping task '%s': %w",
				queueSize, sanitizedDesc, sanitizeError(err))

		case strings.Contains(err.Error(), "queue has been stopped"):
			// Log with action description for better context
			logger.Error("Cannot enqueue task, job queue has been stopped", 
				"task_description", sanitizedDesc)
			return fmt.Errorf("job queue has been stopped, cannot enqueue task '%s': %w",
				sanitizedDesc, sanitizeError(err))

		default:
			// Sanitize error before logging
			sanitizedErr := sanitizeError(err)
			// Double-check that the error message is fully sanitized
			sanitizedErrStr := sanitizeString(sanitizedErr.Error())
			// Log with action description for better context
			logger.Error("Failed to enqueue task", 
				"task_description", sanitizedDesc,
				"error", sanitizedErrStr)
			return fmt.Errorf("failed to enqueue task '%s': %w", sanitizedDesc, sanitizeError(err))
		}
	}

	if p.Settings.Debug {
		speciesName := "unknown"
		if task.Detection.Note.CommonName != "" {
			speciesName = task.Detection.Note.CommonName
		}

		// Log with action description for better context
		logger.Debug("Task enqueued successfully",
			"task_description", sanitizedDesc,
			"job_id", job.ID,
			"species", speciesName)
	}

	return nil
}
