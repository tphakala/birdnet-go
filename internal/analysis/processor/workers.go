// workers.go contains task processing logic for the processor.
package processor

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// TaskType defines types of tasks that can be handled by the worker.
type TaskType int

const (
	TaskTypeAction TaskType = iota // Represents an action task type
)

// Timing constants for operations
const (
	// DefaultEnqueueTimeout is the default timeout for task enqueue operations
	DefaultEnqueueTimeout = 5 * time.Second
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
	// Performance metrics logging pattern
	log := GetLogger()
	startTime := time.Now()
	defer func() {
		log.Debug("Worker pool initialization completed",
			logger.Int64("duration_ms", time.Since(startTime).Milliseconds()),
			logger.Int("max_capacity", p.JobQueue.GetMaxJobs()),
			logger.String("component", "analysis.processor"),
			logger.String("operation", "worker_pool_start"))
	}()

	// State transition logging pattern
	log.Info("Starting worker pool",
		logger.Int("max_capacity", p.JobQueue.GetMaxJobs()),
		logger.String("component", "analysis.processor"))

	// Create a cancellable context for the job queue
	ctx, cancel := context.WithCancel(context.Background())

	// Store the cancel function in the processor for clean shutdown
	p.workerCancel = cancel

	// Ensure the job queue is started with our context
	p.JobQueue.StartWithContext(ctx)

	// State transition logging - final state
	log.Info("Worker pool started successfully",
		logger.Int("max_capacity", p.JobQueue.GetMaxJobs()),
		logger.String("component", "analysis.processor"),
		logger.String("operation", "worker_pool_start"))
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

// EnqueueTask adds a task directly to the job queue for processing.
// Uses context.Background() for backward compatibility.
func (p *Processor) EnqueueTask(task *Task) error {
	return p.EnqueueTaskCtx(context.Background(), task)
}

// EnqueueTaskCtx adds a task directly to the job queue for processing with context.
//
// This method respects the provided context for cancellation and timeouts.
// If the context does not have a deadline, a default timeout of
// DefaultEnqueueTimeout (5 seconds) is automatically applied to prevent
// indefinite blocking during enqueue operations.
//
// Context behavior:
//   - If ctx is already cancelled, returns immediately with cancellation error
//   - If ctx has a deadline, uses it as-is
//   - If ctx has no deadline, wraps with DefaultEnqueueTimeout
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - task: Task to enqueue (must not be nil, with non-nil Action)
//
// Returns error if:
//   - Context is cancelled
//   - Task or Task.Action is nil
//   - Job queue is full or stopped
//   - Enqueue operation fails
func (p *Processor) EnqueueTaskCtx(ctx context.Context, task *Task) error {
	// Check if context is already cancelled before starting
	if ctx.Err() != nil {
		return errors.Newf("task enqueue cancelled: %w", ctx.Err()).
			Component("analysis.processor").
			Category(errors.CategoryCancellation).
			Context("operation", "enqueue_task").
			Context("cancelled_before_start", true).
			Context("retryable", false).
			Build()
	}

	// Performance metrics logging pattern
	log := GetLogger()
	startTime := time.Now()
	defer func() {
		log.Debug("Task enqueue operation completed",
			logger.Int64("duration_ms", time.Since(startTime).Milliseconds()),
			logger.String("component", "analysis.processor"),
			logger.String("operation", "enqueue_task"))
	}()

	// Add timeout if context doesn't have one
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultEnqueueTimeout)
		defer cancel()
	}

	// Validate task parameter
	if task == nil {
		return errors.New(ErrNilTask).
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Context("operation", "enqueue_task").
			Context("validation_type", "nil_task").
			Context("retryable", false).
			Build()
	}

	// Validate the task action
	if task.Action == nil {
		return errors.New(ErrNilAction).
			Component("analysis.processor").
			Category(errors.CategoryValidation).
			Context("operation", "enqueue_task").
			Context("validation_type", "nil_action").
			Context("retryable", false).
			Build()
	}

	// Get action description for logging and error context
	actionDesc := task.Action.GetDescription()
	sanitizedDesc := privacy.ScrubMessage(actionDesc)

	// Get species name for enhanced context
	speciesName := "unknown"
	if task.Detection.Note.CommonName != "" {
		speciesName = task.Detection.Note.CommonName
	}

	// Get retry configuration for the action
	jqRetryConfig := getJobQueueRetryConfig(task.Action)

	// State transition logging - task received
	if p.Settings.Debug {
		log.Debug("Task received for enqueueing",
			logger.String("task_description", sanitizedDesc),
			logger.String("species", speciesName),
			logger.Bool("retry_enabled", jqRetryConfig.Enabled),
			logger.Int("max_retries", jqRetryConfig.MaxRetries),
			logger.String("component", "analysis.processor"))
	}

	// Enqueue the task to the job queue using provided context
	job, err := p.JobQueue.Enqueue(ctx, &ActionAdapter{action: task.Action}, task.Detection, jqRetryConfig)
	if err != nil {
		// Enhanced error handling with specific context using sentinel errors
		switch {
		case errors.Is(err, jobqueue.ErrQueueFull):
			queueSize := p.JobQueue.GetMaxJobs()

			// Enhanced queue full error
			enhancedErr := errors.Newf("job queue is full (capacity: %d): %w", queueSize, err).
				Component("analysis.processor").
				Category(errors.CategoryWorker).
				Context("operation", "enqueue_task").
				Context("error_type", "queue_full").
				Context("queue_capacity", queueSize).
				Context("task_description", sanitizedDesc).
				Context("species", speciesName).
				Context("retryable", true).
				Build()

			log.Warn("Job queue is full, dropping task",
				logger.Int("queue_capacity", queueSize),
				logger.String("task_description", sanitizedDesc),
				logger.String("species", speciesName),
				logger.String("component", "analysis.processor"))

			return enhancedErr

		case errors.Is(err, jobqueue.ErrQueueStopped):
			// Enhanced queue stopped error
			enhancedErr := errors.Newf("cannot enqueue task, job queue stopped: %w", err).
				Component("analysis.processor").
				Category(errors.CategoryWorker).
				Context("operation", "enqueue_task").
				Context("error_type", "queue_stopped").
				Context("task_description", sanitizedDesc).
				Context("species", speciesName).
				Context("retryable", false).
				Build()

			log.Error("Cannot enqueue task, job queue has been stopped",
				logger.String("task_description", sanitizedDesc),
				logger.String("species", speciesName),
				logger.String("component", "analysis.processor"))

			return enhancedErr

		default:
			// Enhanced generic enqueue error
			enhancedErr := errors.Newf("failed to enqueue task: %w", err).
				Component("analysis.processor").
				Category(errors.CategoryWorker).
				Context("operation", "enqueue_task").
				Context("error_type", "enqueue_failure").
				Context("task_description", sanitizedDesc).
				Context("species", speciesName).
				Context("retry_enabled", jqRetryConfig.Enabled).
				Context("retryable", true).
				Build()

			log.Error("Failed to enqueue task",
				logger.String("task_description", sanitizedDesc),
				logger.String("species", speciesName),
				logger.SanitizedError(err),
				logger.String("component", "analysis.processor"))

			return enhancedErr
		}
	}

	// State transition logging - task successfully enqueued
	if p.Settings.Debug {
		log.Debug("Task enqueued successfully",
			logger.String("task_description", sanitizedDesc),
			logger.String("job_id", job.ID),
			logger.String("species", speciesName),
			logger.String("component", "analysis.processor"))
	}

	return nil
}
