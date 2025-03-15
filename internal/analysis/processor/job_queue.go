// job_queue.go
package processor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// Common errors that can be returned by job queue operations
var (
	ErrNilAction     = errors.New("cannot enqueue nil action")
	ErrQueueStopped  = errors.New("job queue has been stopped")
	ErrJobNotFound   = errors.New("job not found in queue")
	ErrInvalidStatus = errors.New("invalid job status")
)

// RetryConfig holds the configuration for retry behavior of an action
type RetryConfig struct {
	Enabled      bool          // Whether retry is enabled for this action
	MaxRetries   int           // Maximum number of retry attempts
	InitialDelay time.Duration // Initial delay before first retry
	MaxDelay     time.Duration // Maximum delay between retries
	Multiplier   float64       // Backoff multiplier for each subsequent retry
}

// Default settings for RetryConfig if not overridden
const (
	DefaultJobLifetime      = 24 * time.Hour   // Jobs older than this are considered stale
	DefaultMaxRetries       = 5                // Maximum number of retry attempts
	DefaultInitialDelay     = 30 * time.Second // Initial delay before first retry
	DefaultMaxDelay         = 30 * time.Minute // Maximum delay between retries
	DefaultMultiplier       = 2.0              // Exponential backoff multiplier
	RetryQueueCheckInterval = 10 * time.Second // How often to check for retry jobs
	ShutdownTimeout         = 30 * time.Second // Maximum time to wait during graceful shutdown
)

// JobStatus represents the current status of a job
type JobStatus int

const (
	JobPending JobStatus = iota
	JobRunning
	JobSucceeded
	JobFailed
	JobStale
	JobArchived // New status for jobs that have been archived
)

// String returns a string representation of JobStatus
func (s JobStatus) String() string {
	switch s {
	case JobPending:
		return "Pending"
	case JobRunning:
		return "Running"
	case JobSucceeded:
		return "Succeeded"
	case JobFailed:
		return "Failed"
	case JobStale:
		return "Stale"
	case JobArchived:
		return "Archived"
	default:
		return "Unknown"
	}
}

// Job represents a unit of work that can be retried
type Job struct {
	ID          string      // Unique ID for this job
	Action      Action      // The action to execute
	Data        interface{} // Data for the action
	Attempts    int         // Number of attempts made so far
	MaxAttempts int         // Maximum number of attempts allowed
	CreatedAt   time.Time   // When the job was created
	NextRetryAt time.Time   // When to next attempt the job
	Status      JobStatus   // Current status of the job
	LastError   error       // Last error encountered
	Config      RetryConfig // Retry configuration for this job
}

// JobQueue manages a queue of jobs that can be retried
type JobQueue struct {
	jobs            []*Job
	archivedJobs    []*Job // Store stale jobs here instead of discarding
	mu              sync.Mutex
	stats           JobStats
	jobCounter      int
	stopCh          chan struct{}
	runningJobs     sync.WaitGroup // Track running jobs for graceful shutdown
	isRunning       bool
	maxArchivedJobs int // Maximum number of archived jobs to keep
}

// JobStats tracks statistics about job executions
type JobStats struct {
	TotalJobs      int
	SuccessfulJobs int
	FailedJobs     int
	StaleJobs      int
	ArchivedJobs   int // Track number of archived jobs
	RetryAttempts  int
	ActionStats    map[string]ActionStats // Key is the type name of the action
	mu             sync.Mutex
}

// JobStatsSnapshot is a copy of JobStats without the mutex, safe for returning
type JobStatsSnapshot struct {
	TotalJobs      int
	SuccessfulJobs int
	FailedJobs     int
	StaleJobs      int
	ArchivedJobs   int
	RetryAttempts  int
	ActionStats    map[string]ActionStats // Key is the type name of the action
}

// ActionStats tracks statistics for a specific action type
type ActionStats struct {
	Attempted  int
	Successful int
	Failed     int
	Retried    int
}

// TypedJob is a generic version of Job with type-safe Data field
type TypedJob[T any] struct {
	ID          string         // Unique ID for this job
	Action      TypedAction[T] // The action to execute
	Data        T              // Data for the action (type-safe)
	Attempts    int            // Number of attempts made so far
	MaxAttempts int            // Maximum number of attempts allowed
	CreatedAt   time.Time      // When the job was created
	NextRetryAt time.Time      // When to next attempt the job
	Status      JobStatus      // Current status of the job
	LastError   error          // Last error encountered
	Config      RetryConfig    // Retry configuration for this job
}

// TypedAction is a generic version of the Action interface with type-safe Data
type TypedAction[T any] interface {
	Execute(data T) error
}

// TypedJobQueue is a generic version of JobQueue for type-safe jobs
type TypedJobQueue[T any] struct {
	JobQueue // Embed the regular JobQueue for shared implementation
}

// NewJobQueue creates a new job queue
func NewJobQueue() *JobQueue {
	return &JobQueue{
		jobs:         make([]*Job, 0),
		archivedJobs: make([]*Job, 0),
		stats: JobStats{
			ActionStats: make(map[string]ActionStats),
		},
		stopCh:          make(chan struct{}),
		isRunning:       false,
		maxArchivedJobs: 1000, // Reasonable default
	}
}

// Start begins processing jobs in the queue with a background context
func (q *JobQueue) Start() {
	q.StartWithContext(context.Background())
}

// StartWithContext begins processing jobs in the queue with the provided context
func (q *JobQueue) StartWithContext(ctx context.Context) {
	q.mu.Lock()
	if q.isRunning {
		q.mu.Unlock()
		return
	}
	q.isRunning = true
	q.mu.Unlock()

	go q.processJobs(ctx)
}

// Stop halts the job queue processing and waits for running jobs to complete
func (q *JobQueue) Stop() error {
	return q.StopWithTimeout(ShutdownTimeout)
}

// StopWithTimeout halts the job queue processing with a timeout for graceful shutdown
func (q *JobQueue) StopWithTimeout(timeout time.Duration) error {
	q.mu.Lock()
	if !q.isRunning {
		q.mu.Unlock()
		return nil
	}

	q.isRunning = false
	close(q.stopCh)
	q.mu.Unlock()

	// Create a context with timeout for waiting
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Set up a channel to signal when all jobs are done
	done := make(chan struct{})
	go func() {
		q.runningJobs.Wait()
		close(done)
	}()

	// Wait for completion or timeout
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown timed out after %v: %w", timeout, ctx.Err())
	}
}

// Enqueue adds a job to the queue
func (q *JobQueue) Enqueue(action Action, data interface{}, config RetryConfig) (*Job, error) {
	if action == nil {
		return nil, ErrNilAction
	}

	q.mu.Lock()
	if !q.isRunning {
		q.mu.Unlock()
		return nil, ErrQueueStopped
	}

	q.jobCounter++
	job := &Job{
		ID:          fmt.Sprintf("job-%d", q.jobCounter),
		Action:      action,
		Data:        data,
		Attempts:    0,
		MaxAttempts: config.MaxRetries,
		CreatedAt:   time.Now(),
		NextRetryAt: time.Now(), // Ready to execute immediately
		Status:      JobPending,
		Config:      config,
	}

	q.jobs = append(q.jobs, job)

	actionType := fmt.Sprintf("%T", action)

	q.stats.mu.Lock()
	q.stats.TotalJobs++
	stats := q.stats.ActionStats[actionType]
	stats.Attempted++
	q.stats.ActionStats[actionType] = stats
	q.stats.mu.Unlock()

	defer q.mu.Unlock()

	log.Printf("Enqueued job %s of type %T for execution", job.ID, action)
	return job, nil
}

// processJobs runs as a goroutine and processes jobs from the queue
func (q *JobQueue) processJobs(ctx context.Context) {
	ticker := time.NewTicker(RetryQueueCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("Job queue processing stopped due to context cancellation: %v", ctx.Err())
			return
		case <-q.stopCh:
			log.Printf("Job queue processing stopped")
			return
		case <-ticker.C:
			// Clean up stale jobs
			q.cleanupStaleJobs()

			// Process due jobs
			q.processDueJobs(ctx)
		}
	}
}

// cleanupStaleJobs moves jobs that have exceeded their lifetime to the archive
func (q *JobQueue) cleanupStaleJobs() {
	q.mu.Lock()
	defer q.mu.Unlock()

	staleTime := time.Now().Add(-DefaultJobLifetime)
	staleCount := 0

	remainingJobs := make([]*Job, 0, len(q.jobs))
	newArchivedJobs := make([]*Job, 0)

	for _, job := range q.jobs {
		if job.Status != JobSucceeded && job.Status != JobFailed && job.CreatedAt.Before(staleTime) {
			staleCount++
			job.Status = JobStale

			// Archive the job instead of discarding it
			newArchivedJobs = append(newArchivedJobs, job)

			q.stats.mu.Lock()
			q.stats.StaleJobs++
			q.stats.ArchivedJobs++
			actionType := fmt.Sprintf("%T", job.Action)
			stats := q.stats.ActionStats[actionType]
			stats.Failed++
			q.stats.ActionStats[actionType] = stats
			q.stats.mu.Unlock()

			log.Printf("Job %s of type %T marked as stale after %v and archived",
				job.ID, job.Action, time.Since(job.CreatedAt))
		} else if job.Status == JobPending || job.Status == JobRunning {
			// Only keep non-terminal jobs in the queue
			remainingJobs = append(remainingJobs, job)
		}
	}

	// Update the active jobs list
	q.jobs = remainingJobs

	// Archive the stale jobs (limited to maxArchivedJobs)
	if len(newArchivedJobs) > 0 {
		// Add new archived jobs
		q.archivedJobs = append(q.archivedJobs, newArchivedJobs...)

		// If we have too many archived jobs, trim the oldest ones
		if len(q.archivedJobs) > q.maxArchivedJobs {
			excess := len(q.archivedJobs) - q.maxArchivedJobs
			q.archivedJobs = q.archivedJobs[excess:]
		}

		log.Printf("Archived %d stale jobs (total archived: %d)",
			staleCount, len(q.archivedJobs))
	}
}

// calculateBackoffDelay computes the next retry delay using exponential backoff
func calculateBackoffDelay(config RetryConfig, attemptNum int) time.Duration {
	if attemptNum <= 0 {
		return config.InitialDelay
	}

	// Calculate exponential backoff: initialDelay * multiplier^(attemptNum-1)
	delay := config.InitialDelay * time.Duration(math.Pow(config.Multiplier, float64(attemptNum-1)))

	// Apply maximum delay cap
	if delay > config.MaxDelay {
		delay = config.MaxDelay
	}

	return delay
}

// processDueJobs processes jobs that are due for execution
func (q *JobQueue) processDueJobs(ctx context.Context) {
	now := time.Now()

	// First, find and mark jobs as running
	var jobsToRun []*Job

	q.mu.Lock()
	for _, job := range q.jobs {
		if job.Status == JobPending && job.NextRetryAt.Before(now) {
			job.Status = JobRunning
			jobsToRun = append(jobsToRun, job)
		}
	}
	q.mu.Unlock()

	// Now process each job
	for _, job := range jobsToRun {
		// Mark that we have a running job
		q.runningJobs.Add(1)

		// Use a separate goroutine to avoid blocking the processor
		go func(j *Job) {
			defer q.runningJobs.Done()
			q.executeJob(ctx, j)
		}(job)
	}
}

// executeJob runs a single job with context
func (q *JobQueue) executeJob(ctx context.Context, job *Job) {
	// Increment attempt counter
	job.Attempts++

	// Update stats
	q.stats.mu.Lock()
	q.stats.RetryAttempts++
	actionType := fmt.Sprintf("%T", job.Action)
	stats := q.stats.ActionStats[actionType]
	stats.Retried++
	q.stats.ActionStats[actionType] = stats
	q.stats.mu.Unlock()

	// Log the attempt
	if job.Attempts > 1 {
		log.Printf("Retrying job %s of type %T, attempt %d/%d",
			job.ID, job.Action, job.Attempts, job.MaxAttempts)
	}

	// Create a timeout context for the job execution
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute the job using a channel to handle timeouts
	errCh := make(chan error, 1)
	go func() {
		// Execute the actual action with its data
		errCh <- job.Action.Execute(job.Data)
	}()

	// Wait for completion or timeout
	var err error
	select {
	case err = <-errCh:
		// Normal completion
	case <-execCtx.Done():
		// Context timeout or cancellation
		err = fmt.Errorf("job execution timed out or was cancelled: %w", execCtx.Err())
	}

	// Handle the result
	q.mu.Lock()
	defer q.mu.Unlock()

	if err != nil {
		// Job failed
		job.LastError = err

		if job.Attempts >= job.MaxAttempts {
			// No more retries
			job.Status = JobFailed

			q.stats.mu.Lock()
			q.stats.FailedJobs++
			stats := q.stats.ActionStats[actionType]
			stats.Failed++
			q.stats.ActionStats[actionType] = stats
			q.stats.mu.Unlock()

			log.Printf("Job %s of type %T permanently failed after %d attempts: %v",
				job.ID, job.Action, job.Attempts, err)
		} else {
			// Schedule for retry
			job.Status = JobPending

			// Calculate backoff with exponential strategy
			delay := calculateBackoffDelay(job.Config, job.Attempts)
			job.NextRetryAt = time.Now().Add(delay)

			log.Printf("Job %s of type %T failed, will retry in %v (attempt %d/%d): %v",
				job.ID, job.Action, delay, job.Attempts, job.MaxAttempts, err)
		}
	} else {
		// Job succeeded
		job.Status = JobSucceeded

		q.stats.mu.Lock()
		q.stats.SuccessfulJobs++
		stats := q.stats.ActionStats[actionType]
		stats.Successful++
		q.stats.ActionStats[actionType] = stats
		q.stats.mu.Unlock()

		if job.Attempts > 1 {
			log.Printf("Job %s of type %T succeeded after %d attempts",
				job.ID, job.Action, job.Attempts)
		}
	}
}

// GetStats returns a copy of the current stats without the mutex
func (q *JobQueue) GetStats() JobStatsSnapshot {
	q.stats.mu.Lock()
	defer q.stats.mu.Unlock()

	// Make a deep copy of the stats
	statsCopy := JobStatsSnapshot{
		TotalJobs:      q.stats.TotalJobs,
		SuccessfulJobs: q.stats.SuccessfulJobs,
		FailedJobs:     q.stats.FailedJobs,
		StaleJobs:      q.stats.StaleJobs,
		ArchivedJobs:   q.stats.ArchivedJobs,
		RetryAttempts:  q.stats.RetryAttempts,
		ActionStats:    make(map[string]ActionStats),
	}

	for k, v := range q.stats.ActionStats {
		statsCopy.ActionStats[k] = v
	}

	return statsCopy
}

// GetDefaultRetryConfig returns a default retry configuration
func GetDefaultRetryConfig(enabled bool) RetryConfig {
	return RetryConfig{
		Enabled:      enabled,
		MaxRetries:   DefaultMaxRetries,
		InitialDelay: DefaultInitialDelay,
		MaxDelay:     DefaultMaxDelay,
		Multiplier:   DefaultMultiplier,
	}
}

// NewTypedJobQueue creates a new typed job queue
func NewTypedJobQueue[T any]() *TypedJobQueue[T] {
	return &TypedJobQueue[T]{
		JobQueue: *NewJobQueue(),
	}
}

// EnqueueTyped adds a type-safe job to the queue
func (q *TypedJobQueue[T]) EnqueueTyped(action TypedAction[T], data T, config RetryConfig) (*TypedJob[T], error) {
	if action == nil {
		return nil, ErrNilAction
	}

	// Create an adapter from TypedAction to Action
	adapter := &typedActionAdapter[T]{
		action: action,
		data:   data,
	}

	// Enqueue using the regular JobQueue
	job, err := q.JobQueue.Enqueue(adapter, nil, config)
	if err != nil {
		return nil, err
	}

	// Create a TypedJob representation
	typedJob := &TypedJob[T]{
		ID:          job.ID,
		Action:      action,
		Data:        data,
		Attempts:    job.Attempts,
		MaxAttempts: job.MaxAttempts,
		CreatedAt:   job.CreatedAt,
		NextRetryAt: job.NextRetryAt,
		Status:      job.Status,
		LastError:   job.LastError,
		Config:      job.Config,
	}

	return typedJob, nil
}

// typedActionAdapter adapts a TypedAction to the Action interface
type typedActionAdapter[T any] struct {
	action TypedAction[T]
	data   T
}

// Execute implements the Action interface for typedActionAdapter
func (a *typedActionAdapter[T]) Execute(data interface{}) error {
	// Ignore the data parameter and use the stored typed data instead
	return a.action.Execute(a.data)
}
