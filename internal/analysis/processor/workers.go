// workers.go contains task processing logic for the processor.
package processor

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
)

// TaskType defines types of tasks that can be handled by the worker.
type TaskType int

const (
	TaskTypeAction TaskType = iota // Represents an action task type
)

// Task represents a unit of work, encapsulating the detection and the action to be performed.
type Task struct {
	Type      TaskType
	Detection Detections
	Action    Action
}

// startWorkerPool initializes the job queue for task processing.
// This is kept for backward compatibility but now simply ensures the job queue is started.
func (p *Processor) startWorkerPool(numWorkers int) {
	// Create a cancellable context for the job queue
	ctx, cancel := context.WithCancel(context.Background())

	// Store the cancel function in the processor for clean shutdown
	p.workerCancel = cancel

	// Ensure the job queue is started with our context
	p.JobQueue.StartWithContext(ctx)

	log.Printf("Job queue started with max capacity of %d jobs", p.JobQueue.GetMaxJobs())
}

// getJobQueueRetryConfig extracts the retry configuration from an action
func getJobQueueRetryConfig(action Action) jobqueue.RetryConfig {
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
func EnqueueTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("cannot enqueue nil task")
	}

	if p == nil {
		return fmt.Errorf("processor not initialized, call InitProcessor first")
	}

	// Get action type for logging
	actionType := fmt.Sprintf("%T", task.Action)

	// Validate the task
	if task.Action == nil {
		return fmt.Errorf("cannot enqueue task with nil action")
	}

	// Get retry configuration for the action directly as jobqueue.RetryConfig
	jqRetryConfig := getJobQueueRetryConfig(task.Action)

	// Log detailed information about the task being enqueued
	if p.Settings.Debug {
		log.Printf("Enqueuing task for action type %s with retry config: enabled=%v, maxRetries=%d",
			actionType, jqRetryConfig.Enabled, jqRetryConfig.MaxRetries)
	}

	// Enqueue the task directly to the job queue
	job, err := p.JobQueue.Enqueue(&ActionAdapter{action: task.Action}, task.Detection, jqRetryConfig)
	if err != nil {
		// Handle specific error types with appropriate messages
		switch {
		case strings.Contains(err.Error(), "queue is full"):
			queueSize := p.JobQueue.GetMaxJobs()
			log.Printf("❌ Job queue is full (capacity: %d), dropping task for action type %s",
				queueSize, actionType)

			// Suggest increasing queue size if this happens frequently
			return fmt.Errorf("job queue is full (capacity: %d): %w", queueSize, err)

		case strings.Contains(err.Error(), "queue has been stopped"):
			log.Printf("❌ Cannot enqueue task for action type %s: job queue has been stopped",
				actionType)
			return fmt.Errorf("job queue has been stopped, cannot enqueue task for %s: %w",
				actionType, err)

		default:
			log.Printf("❌ Failed to enqueue task for action type %s: %v", actionType, err)
			return fmt.Errorf("failed to enqueue task for %s: %w", actionType, err)
		}
	}

	if p.Settings.Debug {
		speciesName := "unknown"
		if task.Detection.Note.CommonName != "" {
			speciesName = task.Detection.Note.CommonName
		}

		log.Printf("✅ Task enqueued as job %s for action type %s (species: %s)",
			job.ID, actionType, speciesName)
	}

	return nil
}
