// workers.go contains the worker pool and the worker goroutines that process tasks.
package processor

import (
	"context"
	"fmt"
	"log"
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

// workerQueue is a channel that holds tasks to be processed by worker goroutines.
var workerQueue chan Task

// startWorkerPool initializes a pool of worker goroutines to process tasks.
func (p *Processor) startWorkerPool(numWorkers int) {
	workerQueue = make(chan Task, 100) // Initialize the task queue with a buffer

	// Start the specified number of worker goroutines
	for i := 0; i < numWorkers; i++ {
		go p.actionWorker(context.Background())
	}
}

// actionWorker is the goroutine that processes tasks from the workerQueue.
func (p *Processor) actionWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("Worker stopping due to context cancellation: %v", ctx.Err())
			return
		case task, ok := <-workerQueue:
			if !ok {
				// Channel closed, exit worker
				return
			}

			if task.Type == TaskTypeAction {
				// Check if the action has retry enabled
				retryConfig := getRetryConfig(task.Action)

				// Execute the action associated with the task
				err := task.Action.Execute(task.Detection)
				if err != nil {
					// Check if we should retry this action
					if retryConfig.Enabled {
						actionType := fmt.Sprintf("%T", task.Action)
						log.Printf("Action %s failed: %v, adding to retry queue", actionType, err)
						// Add to the retry queue
						_, enqueueErr := p.JobQueue.Enqueue(task.Action, task.Detection, retryConfig)
						if enqueueErr != nil {
							log.Printf("Failed to enqueue retry job: %v", enqueueErr)
						}
					} else {
						log.Printf("Action %T failed (no retry): %v", task.Action, err)
					}
				}
			}
		}
	}
}

// getRetryConfig extracts the retry configuration from an action if available
func getRetryConfig(action Action) RetryConfig {
	switch a := action.(type) {
	case *BirdWeatherAction:
		return a.RetryConfig
	case *MqttAction:
		return a.RetryConfig
	default:
		// Default no retry for actions that don't support it
		return RetryConfig{Enabled: false}
	}
}

// EnqueueTask adds a task to the worker queue for processing.
func EnqueueTask(task *Task) error {
	if task == nil {
		return fmt.Errorf("cannot enqueue nil task")
	}

	select {
	case workerQueue <- *task:
		// Task enqueued successfully
		return nil
	default:
		err := fmt.Errorf("worker queue is full")
		log.Printf("❌ %v", err)
		return err
	}
}
