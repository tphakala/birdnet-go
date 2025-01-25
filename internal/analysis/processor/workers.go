// workers.go contains the worker pool and the worker goroutines that process tasks.
package processor

import (
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

// StartWorkerPool initializes a pool of worker goroutines to process tasks.
func (p *Processor) startWorkerPool(numWorkers int) {
	workerQueue = make(chan Task, 100) // Initialize the task queue with a buffer

	// Start the specified number of worker goroutines
	for i := 0; i < numWorkers; i++ {
		go p.actionWorker()
	}
}

// actionWorker is the goroutine that processes tasks from the workerQueue.
func (p *Processor) actionWorker() {
	for task := range workerQueue {
		if task.Type == TaskTypeAction {
			// Execute the action associated with the task
			err := task.Action.Execute(task.Detection)
			if err != nil {
				log.Printf("Error executing action: %s\n", err)
				// Handle errors appropriately (e.g., log, retry)
			}
		}
	}
}
