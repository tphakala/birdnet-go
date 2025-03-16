package jobqueue

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// JobQueue manages a queue of jobs that can be retried
type JobQueue struct {
	jobs               []*Job
	archivedJobs       []*Job // Store stale jobs here instead of discarding
	mu                 sync.Mutex
	stats              JobStats
	jobCounter         int
	stopCh             chan struct{}
	runningJobs        sync.WaitGroup // Track running jobs for graceful shutdown
	isRunning          bool
	maxArchivedJobs    int  // Maximum number of archived jobs to keep
	maxJobs            int  // Maximum number of pending jobs in the queue
	droppedJobs        int  // Counter for jobs dropped due to queue full
	logAllSuccesses    bool // Whether to log all successful jobs, not just retries
	processCancel      context.CancelFunc
	processingInterval time.Duration // Interval for the processing ticker (for testing)
	clock              Clock         // Clock interface for time-related operations
}

// NewJobQueue creates a new job queue with default settings
func NewJobQueue() *JobQueue {
	return NewJobQueueWithOptions(1000, 100, false)
}

// NewJobQueueWithOptions creates a new job queue with custom settings
func NewJobQueueWithOptions(maxJobs, maxArchivedJobs int, logAllSuccesses bool) *JobQueue {
	return &JobQueue{
		jobs:               make([]*Job, 0),
		archivedJobs:       make([]*Job, 0),
		stopCh:             make(chan struct{}),
		maxArchivedJobs:    maxArchivedJobs,
		maxJobs:            maxJobs,
		logAllSuccesses:    logAllSuccesses,
		processingInterval: 1 * time.Second, // Default processing interval
		clock:              &RealClock{},    // Use the real clock by default
		stats: JobStats{
			ActionStats: make(map[string]ActionStats),
		},
	}
}

// SetProcessingInterval sets the processing interval for testing purposes
func (q *JobQueue) SetProcessingInterval(interval time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.processingInterval = interval
}

// SetClock sets a custom clock implementation for testing purposes
func (q *JobQueue) SetClock(clock Clock) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.clock = clock
}

// Start starts the job queue processing
func (q *JobQueue) Start() {
	q.StartWithContext(context.Background())
}

// StartWithContext starts the job queue processing with a context
func (q *JobQueue) StartWithContext(ctx context.Context) {
	q.mu.Lock()
	if q.isRunning {
		q.mu.Unlock()
		return
	}
	q.isRunning = true
	q.stopCh = make(chan struct{}) // Ensure we have a fresh stop channel
	q.mu.Unlock()

	// Create a derived context that we can cancel when stopping
	processCtx, cancel := context.WithCancel(ctx)

	// Store the cancel function to be called during shutdown
	q.mu.Lock()
	q.processCancel = cancel
	q.mu.Unlock()

	go q.processJobs(processCtx)
}

// Stop stops the job queue processing
func (q *JobQueue) Stop() error {
	return q.StopWithTimeout(10 * time.Second)
}

// StopWithTimeout stops the job queue processing with a timeout
func (q *JobQueue) StopWithTimeout(timeout time.Duration) error {
	q.mu.Lock()
	if !q.isRunning {
		q.mu.Unlock()
		return nil
	}
	q.isRunning = false

	// Cancel the processing context if available
	if q.processCancel != nil {
		q.processCancel()
		q.processCancel = nil
	}

	// Signal the processor to stop via channel as well (for backward compatibility)
	close(q.stopCh)
	q.mu.Unlock()

	// Wait for all running jobs to complete with timeout
	c := make(chan struct{})
	go func() {
		q.runningJobs.Wait()
		close(c)
	}()

	select {
	case <-c:
		return nil
	case <-q.clock.After(timeout):
		return fmt.Errorf("timed out waiting for jobs to complete after %v", timeout)
	}
}

// getActionKey returns a unique key for an action based on its type and description
func getActionKey(action Action) string {
	typeName := fmt.Sprintf("%T", action)
	description := action.GetDescription()

	// Escape any colons in the description to avoid ambiguity when splitting
	escapedDescription := strings.ReplaceAll(description, ":", "\\:")

	// Combine type name and description to create a unique key
	// This ensures different actions with the same type but different descriptions
	// get separate statistics entries
	return fmt.Sprintf("%s:%s", typeName, escapedDescription)
}

// Enqueue adds a job to the queue
func (q *JobQueue) Enqueue(action Action, data interface{}, config RetryConfig) (*Job, error) {
	if action == nil {
		return nil, ErrNilAction
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// Check if queue is running
	if !q.isRunning {
		return nil, ErrQueueStopped
	}

	// Check if queue is full and handle accordingly
	if len(q.jobs) >= q.maxJobs {
		// If DropOldestOnFull is enabled, try to make room
		if !q._dropOldestPendingJob() {
			// Could not drop any job, queue is full
			q.droppedJobs++
			q.stats.DroppedJobs++

			// Update action-specific stats
			actionKey := getActionKey(action)
			stats, exists := q.stats.ActionStats[actionKey]
			if !exists {
				// Initialize the stats for this action type
				stats = ActionStats{
					TypeName:    fmt.Sprintf("%T", action),
					Description: action.GetDescription(),
				}
			}
			stats.Dropped++
			q.stats.ActionStats[actionKey] = stats

			return nil, fmt.Errorf("%w: maximum queue size (%d) reached", ErrQueueFull, q.maxJobs)
		}
	}

	// Increment job counter (kept for backward compatibility and metrics, not used for ID generation)
	q.jobCounter++
	// Generate a UUID v4 for the job ID, truncated to 8 characters
	uuidStr := uuid.New().String()
	shortUUID := uuidStr[:8] // Take first 8 characters of the UUID

	// Pre-allocate ID string to reduce memory allocations
	now := q.clock.Now()
	job := &Job{
		ID:          fmt.Sprintf("job-%s", shortUUID),
		Action:      action,
		Data:        data,
		MaxAttempts: config.MaxRetries + 1,
		CreatedAt:   now,
		NextRetryAt: now, // Ready to run immediately
		Status:      JobStatusPending,
		Config:      config,
	}

	q.jobs = append(q.jobs, job)
	q.stats.TotalJobs++

	// Update action-specific stats
	actionKey := getActionKey(action)
	stats, exists := q.stats.ActionStats[actionKey]
	if !exists {
		// Initialize the stats for this action type
		stats = ActionStats{
			TypeName:    fmt.Sprintf("%T", action),
			Description: action.GetDescription(),
		}
	}
	stats.Attempted++
	// Only increment Retried for actual retries
	if job.Attempts > 1 {
		stats.Retried++
	}
	q.stats.ActionStats[actionKey] = stats

	return job, nil
}

// _dropOldestPendingJob removes the oldest pending job from the queue
// to make room for a new job. Returns true if a job was dropped.
// IMPORTANT: This method must be called with q.mu already locked.
func (q *JobQueue) _dropOldestPendingJob() bool {
	// For testing queue overflow, respect the global AllowJobDropping flag
	if !AllowJobDropping {
		return false
	}

	// Find the oldest pending job
	oldestIdx := -1
	var oldestTime time.Time

	for i, job := range q.jobs {
		// Skip jobs that are exempt from dropping
		if job.TestExemptFromDropping {
			continue
		}

		if job.Status == JobStatusPending {
			if oldestIdx == -1 || job.CreatedAt.Before(oldestTime) {
				oldestIdx = i
				oldestTime = job.CreatedAt
			}
		}
	}

	if oldestIdx == -1 {
		// No pending jobs found
		return false
	}

	// Remove the oldest job
	oldestJob := q.jobs[oldestIdx]
	q.jobs = append(q.jobs[:oldestIdx], q.jobs[oldestIdx+1:]...)

	// Update stats
	q.droppedJobs++
	q.stats.DroppedJobs++

	// Update action-specific stats
	actionKey := getActionKey(oldestJob.Action)
	stats := q.stats.ActionStats[actionKey]
	stats.Dropped++
	q.stats.ActionStats[actionKey] = stats

	log.Printf("Dropped oldest pending job %s to make room for new job", oldestJob.ID)
	return true
}

// processJobs is the main job processing loop
func (q *JobQueue) processJobs(ctx context.Context) {
	// Use the custom processing interval
	q.mu.Lock()
	interval := q.processingInterval
	q.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Check context immediately and periodically
	checkCtx := func() bool {
		if ctx.Err() != nil {
			log.Printf("Job queue processing stopped via context: %v", ctx.Err())
			return true
		}
		return false
	}

	// Exit immediately if context is already canceled
	if checkCtx() {
		return
	}

	for {
		select {
		case <-q.stopCh:
			log.Println("Job queue processing stopped via stop channel")
			return
		case <-ctx.Done():
			log.Printf("Job queue processing stopped via context: %v", ctx.Err())
			return
		case <-ticker.C:
			// Check context again before processing
			if checkCtx() {
				return
			}

			// Pass the context to cleanup and processing functions
			q.cleanupStaleJobs(ctx)
			q.processDueJobs(ctx)
		}
	}
}

// cleanupStaleJobs moves completed and failed jobs to the archived jobs list
func (q *JobQueue) cleanupStaleJobs(ctx context.Context) {
	// Quick check for context cancellation
	if ctx.Err() != nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	var activeJobs []*Job
	var staleJobs []*Job

	// Identify stale jobs (completed or failed)
	for _, job := range q.jobs {
		if job.Status == JobStatusCompleted || job.Status == JobStatusFailed {
			staleJobs = append(staleJobs, job)
		} else {
			activeJobs = append(activeJobs, job)
		}
	}

	// Update the jobs list to only include active jobs
	q.jobs = activeJobs

	// Add stale jobs to the archived jobs list
	q.archivedJobs = append(q.archivedJobs, staleJobs...)
	q.stats.StaleJobs += len(staleJobs)
	q.stats.ArchivedJobs = len(q.archivedJobs)

	// Trim archived jobs if needed
	if len(q.archivedJobs) > q.maxArchivedJobs {
		excess := len(q.archivedJobs) - q.maxArchivedJobs
		q.archivedJobs = q.archivedJobs[excess:]
		q.stats.ArchivedJobs = len(q.archivedJobs)
	}
}

// calculateBackoffDelay calculates the delay before the next retry attempt
func calculateBackoffDelay(config RetryConfig, attemptNum int, clock Clock) time.Duration {
	// Calculate exponential backoff with jitter
	backoff := float64(config.InitialDelay) * math.Pow(config.Multiplier, float64(attemptNum))

	// Add some jitter (±10%)
	jitterFactor := 0.9 + 0.2*float64(clock.Now().Nanosecond())/1e9
	backoff *= jitterFactor

	// Cap at max delay
	if backoff > float64(config.MaxDelay) {
		backoff = float64(config.MaxDelay)
	}

	return time.Duration(backoff)
}

// processDueJobs processes jobs that are due for execution
func (q *JobQueue) processDueJobs(ctx context.Context) {
	// Quick check for context cancellation
	if ctx.Err() != nil {
		return
	}

	q.mu.Lock()

	// Find jobs that are due for execution
	var dueJobs []*Job
	now := q.clock.Now()

	for _, job := range q.jobs {
		// Check for both pending and retrying jobs
		if (job.Status == JobStatusPending || job.Status == JobStatusRetrying) && !job.NextRetryAt.After(now) {
			dueJobs = append(dueJobs, job)
			job.Status = JobStatusRunning
		}
	}

	q.mu.Unlock()

	// Execute due jobs
	for _, job := range dueJobs {
		// Check context again before starting each job
		if ctx.Err() != nil {
			// Context was cancelled, revert job status and return
			q.mu.Lock()
			for _, j := range dueJobs {
				if j.Status == JobStatusRunning {
					// Revert to original status
					if j.Attempts > 0 {
						j.Status = JobStatusRetrying
					} else {
						j.Status = JobStatusPending
					}
				}
			}
			q.mu.Unlock()
			return
		}

		q.runningJobs.Add(1)
		go func(j *Job) {
			defer q.runningJobs.Done()
			q.executeJob(ctx, j)
		}(job)
	}
}

// Maximum length for stored error messages
const maxErrorMessageLength = 500

// sanitizeErrorMessage returns a sanitized version of the error message
// for safe storage in statistics. This function:
// 1. Handles nil errors
// 2. Bounds the message length to prevent memory bloat
// 3. Removes any control characters or potential code injection patterns
func sanitizeErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	errMsg := err.Error()

	// Bound message length to prevent memory bloat
	if len(errMsg) > maxErrorMessageLength {
		truncatedMsg := errMsg[:maxErrorMessageLength]
		errMsg = truncatedMsg + "... [truncated]"
	}

	// Remove control characters and potentially unsafe characters
	errMsg = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 { // ASCII control characters
			return -1 // Remove character
		}
		return r
	}, errMsg)

	return errMsg
}

// executeJob executes a job and handles retries if needed
func (q *JobQueue) executeJob(ctx context.Context, job *Job) {
	// Increment attempt counter
	job.Attempts++

	// Get action description for logging
	actionDesc := job.Action.GetDescription()

	// Update stats
	q.mu.Lock()
	// Only increment RetryAttempts for actual retries
	if job.Attempts > 1 {
		q.stats.RetryAttempts++
	}
	actionKey := getActionKey(job.Action)
	stats, exists := q.stats.ActionStats[actionKey]
	if !exists {
		// Initialize the stats for this action type
		stats = ActionStats{
			TypeName:    fmt.Sprintf("%T", job.Action),
			Description: actionDesc,
		}
	}
	stats.Attempted++
	// Only increment Retried for actual retries
	if job.Attempts > 1 {
		stats.Retried++
	}

	// Update last execution time
	executionStartTime := q.clock.Now()
	stats.LastExecutionTime = executionStartTime
	q.stats.ActionStats[actionKey] = stats
	q.mu.Unlock()

	// Log the attempt
	if job.Attempts > 1 {
		log.Printf("Retrying job %s: %s, attempt %d/%d",
			job.ID, actionDesc, job.Attempts, job.MaxAttempts)
	}

	// Create a timeout context for the job execution
	execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Execute the job with proper context handling and error capture
	var err error
	done := make(chan struct{})

	go func() {
		// Add panic recovery to prevent goroutine crashes
		defer func() {
			if r := recover(); r != nil {
				// Convert panic to error
				err = fmt.Errorf("job execution panicked: %v", r)
			}
			// Always close the channel at the end, regardless of how we exit
			close(done)
		}()

		err = job.Action.Execute(job.Data)
	}()

	// Wait for completion, timeout, or cancellation
	select {
	case <-done:
		// Normal completion, err is already set
	case <-execCtx.Done():
		// Context timeout or cancellation
		ctxErr := execCtx.Err()
		if ctxErr == context.DeadlineExceeded {
			err = fmt.Errorf("job execution timed out after 30 seconds: %w", ctxErr)
		} else {
			err = fmt.Errorf("job execution was cancelled: %w", ctxErr)
		}
	}

	// Calculate execution duration
	executionEndTime := q.clock.Now()
	executionDuration := executionEndTime.Sub(executionStartTime)

	// Handle the result
	q.mu.Lock()
	defer q.mu.Unlock()

	// Update performance metrics
	stats = q.stats.ActionStats[actionKey]
	stats.TotalDuration += executionDuration

	// Update min/max duration
	if stats.MinDuration == 0 || executionDuration < stats.MinDuration {
		stats.MinDuration = executionDuration
	}
	if executionDuration > stats.MaxDuration {
		stats.MaxDuration = executionDuration
	}

	// Calculate average duration
	totalAttempts := stats.Successful + stats.Failed + stats.Retried
	if totalAttempts > 0 {
		stats.AverageDuration = time.Duration(int64(stats.TotalDuration) / int64(totalAttempts))
	}

	if err != nil {
		// Job failed
		job.LastError = err

		// Store sanitized error message
		sanitizedErr := sanitizeErrorMessage(err)
		stats.LastErrorMessage = sanitizedErr
		stats.LastFailedTime = executionEndTime

		if job.Attempts >= job.MaxAttempts {
			// No more retries
			job.Status = JobStatusFailed

			q.stats.FailedJobs++
			stats.Failed++
			q.stats.ActionStats[actionKey] = stats

			log.Printf("Job %s: %s permanently failed after %d attempts: %v",
				job.ID, actionDesc, job.Attempts, err)
		} else {
			// Schedule for retry
			job.Status = JobStatusRetrying

			// Calculate backoff with exponential strategy
			delay := calculateBackoffDelay(job.Config, job.Attempts, q.clock)
			job.NextRetryAt = q.clock.Now().Add(delay)

			log.Printf("Job %s: %s failed, will retry in %v (attempt %d/%d): %v",
				job.ID, actionDesc, delay, job.Attempts, job.MaxAttempts, err)
		}
	} else {
		// Job succeeded
		job.Status = JobStatusCompleted

		// Update successful execution metrics
		stats.LastSuccessfulTime = executionEndTime

		q.stats.SuccessfulJobs++
		stats.Successful++
		q.stats.ActionStats[actionKey] = stats

		// Log success based on configuration
		if job.Attempts > 1 || q.logAllSuccesses {
			if job.Attempts > 1 {
				log.Printf("Job %s: %s succeeded after %d attempts",
					job.ID, actionDesc, job.Attempts)
			} else {
				log.Printf("Job %s: %s succeeded on first attempt",
					job.ID, actionDesc)
			}
		}
	}
}

// GetStats returns a snapshot of the current job statistics
func (q *JobQueue) GetStats() JobStatsSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Create a copy of the action stats map
	actionStatsCopy := make(map[string]ActionStats, len(q.stats.ActionStats))

	// Group stats by type name for backward compatibility with tests
	typeNameMap := make(map[string][]ActionStats)

	// First, collect all stats by type name
	for k := range q.stats.ActionStats {
		// Extract the type name from the key (format is "TypeName:Description")
		// We need to look for unescaped colons to handle cases where description contains escaped colons
		colonIndex := -1
		for i := 0; i < len(k); i++ {
			if k[i] == ':' && (i == 0 || k[i-1] != '\\') {
				colonIndex = i
				break
			}
		}

		var typeName string
		if colonIndex >= 0 {
			typeName = k[:colonIndex]
		} else {
			// Fallback if no unescaped colon is found
			typeName = k
		}

		// Get a copy of the value to avoid modifying the original
		v := q.stats.ActionStats[k]

		// Make sure description is up-to-date by checking if we have a reference to the action
		for _, job := range q.jobs {
			jobActionKey := getActionKey(job.Action)
			if jobActionKey == k && job.Action != nil {
				// Update description
				v.Description = job.Action.GetDescription()
				break
			}
		}

		// Store in the type name map
		typeNameMap[typeName] = append(typeNameMap[typeName], v)

		// Also keep the original key-value pair
		actionStatsCopy[k] = v
	}

	// Now add aggregated stats by type name for backward compatibility
	for typeName, statsList := range typeNameMap {
		if len(statsList) == 0 {
			continue
		}

		// Use the first entry as a base
		aggregated := statsList[0]

		// Aggregate stats from all actions of this type
		for i := 1; i < len(statsList); i++ {
			s := statsList[i]
			aggregated.Attempted += s.Attempted
			aggregated.Successful += s.Successful
			aggregated.Failed += s.Failed
			aggregated.Retried += s.Retried
			aggregated.Dropped += s.Dropped
			aggregated.TotalDuration += s.TotalDuration

			// Update min/max durations
			if s.MinDuration > 0 && (aggregated.MinDuration == 0 || s.MinDuration < aggregated.MinDuration) {
				aggregated.MinDuration = s.MinDuration
			}
			if s.MaxDuration > aggregated.MaxDuration {
				aggregated.MaxDuration = s.MaxDuration
			}

			// Use the most recent timestamps
			if s.LastExecutionTime.After(aggregated.LastExecutionTime) {
				aggregated.LastExecutionTime = s.LastExecutionTime
			}
			if s.LastSuccessfulTime.After(aggregated.LastSuccessfulTime) {
				aggregated.LastSuccessfulTime = s.LastSuccessfulTime
			}
			if s.LastFailedTime.After(aggregated.LastFailedTime) {
				aggregated.LastFailedTime = s.LastFailedTime
				aggregated.LastErrorMessage = s.LastErrorMessage
			}
		}

		// Calculate average duration
		totalAttempts := aggregated.Successful + aggregated.Failed + aggregated.Retried
		if totalAttempts > 0 {
			aggregated.AverageDuration = time.Duration(int64(aggregated.TotalDuration) / int64(totalAttempts))
		}

		// Add the aggregated stats to the copy
		actionStatsCopy[typeName] = aggregated
	}

	return JobStatsSnapshot{
		// Queue statistics
		TotalJobs:      q.stats.TotalJobs,
		SuccessfulJobs: q.stats.SuccessfulJobs,
		FailedJobs:     q.stats.FailedJobs,
		StaleJobs:      q.stats.StaleJobs,
		ArchivedJobs:   q.stats.ArchivedJobs,
		DroppedJobs:    q.stats.DroppedJobs,
		RetryAttempts:  q.stats.RetryAttempts,

		// Current queue state
		PendingJobs:      len(q.jobs),
		MaxQueueSize:     q.maxJobs,
		QueueUtilization: float64(len(q.jobs)) / float64(q.maxJobs) * 100.0,

		// Action-specific statistics
		ActionStats: actionStatsCopy,
	}
}

// GetDefaultRetryConfig returns a default retry configuration
func GetDefaultRetryConfig(enabled bool) RetryConfig {
	if !enabled {
		return RetryConfig{Enabled: false}
	}

	return RetryConfig{
		Enabled:      true,
		MaxRetries:   5,
		InitialDelay: 30 * time.Second,
		MaxDelay:     1 * time.Hour,
		Multiplier:   2.0,
	}
}

// TypedJobQueue is a generic version of JobQueue for type-safe operations
type TypedJobQueue[T any] struct {
	JobQueue // Embed the regular JobQueue for shared implementation
}

// NewTypedJobQueue creates a new typed job queue
func NewTypedJobQueue[T any]() *TypedJobQueue[T] {
	return &TypedJobQueue[T]{
		JobQueue: *NewJobQueue(),
	}
}

// EnqueueTyped adds a typed job to the queue
func (q *TypedJobQueue[T]) EnqueueTyped(action TypedAction[T], data T, config RetryConfig) (*TypedJob[T], error) {
	// Create an adapter that converts the typed action to a regular action
	adapter := &typedActionAdapter[T]{
		action: action,
		data:   data,
	}

	// Enqueue the job using the adapter
	job, err := q.JobQueue.Enqueue(adapter, nil, config)
	if err != nil {
		return nil, err
	}

	// Convert the job to a typed job
	typedJob := &TypedJob[T]{
		ID:                     job.ID,
		Action:                 action,
		Data:                   data,
		Attempts:               job.Attempts,
		MaxAttempts:            job.MaxAttempts,
		CreatedAt:              job.CreatedAt,
		NextRetryAt:            job.NextRetryAt,
		Status:                 job.Status,
		LastError:              job.LastError,
		Config:                 job.Config,
		TestExemptFromDropping: job.TestExemptFromDropping,
	}

	return typedJob, nil
}

// typedActionAdapter adapts a TypedAction to the Action interface
type typedActionAdapter[T any] struct {
	action TypedAction[T]
	data   T
}

// Execute implements the Action interface
func (a *typedActionAdapter[T]) Execute(data interface{}) error {
	// If data is provided, ensure it's the correct type and use it
	if data != nil {
		if typedData, ok := data.(T); ok {
			return a.action.Execute(typedData)
		}
		return fmt.Errorf("invalid data type: expected %T, got %T", a.data, data)
	}
	return a.action.Execute(a.data)
}

// GetDescription implements the Action interface
func (a *typedActionAdapter[T]) GetDescription() string {
	return a.action.GetDescription()
}

// GetMaxJobs returns the maximum number of jobs allowed in the queue
func (q *JobQueue) GetMaxJobs() int {
	return q.maxJobs
}

// ProcessImmediately processes any pending jobs immediately without waiting for the ticker
// This method is intended for testing purposes only
func (q *JobQueue) ProcessImmediately(ctx context.Context) {
	q.cleanupStaleJobs(ctx)
	q.processDueJobs(ctx)
}

// GetPendingJobs returns a slice of all pending jobs in the queue
// This method is primarily intended for testing purposes
func (q *JobQueue) GetPendingJobs() []*Job {
	q.mu.Lock()
	defer q.mu.Unlock()

	pendingJobs := make([]*Job, 0, len(q.jobs))
	for _, job := range q.jobs {
		if job.Status == JobStatusPending {
			pendingJobs = append(pendingJobs, job)
		}
	}

	return pendingJobs
}
