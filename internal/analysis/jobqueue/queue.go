package jobqueue

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Configuration constants
const (
	// DefaultJobExecutionTimeout is the default timeout for job execution
	DefaultJobExecutionTimeout = 30 * time.Second

	// MaxActionStatsEntries is the maximum number of action stats to keep in memory
	// Older entries will be removed to prevent unbounded memory growth
	MaxActionStatsEntries = 1000

	// ActionStatsTargetSize is the target size after cleanup (with hysteresis margin)
	// Set to 80% of max to avoid repeated cleanup triggers
	ActionStatsTargetSize = int(MaxActionStatsEntries * 0.8)
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
		return errors.Newf("timed out waiting for jobs to complete after %v", timeout).
			Component("analysis.jobqueue").
			Category(errors.CategoryTimeout).
			Context("operation", "stop_with_timeout").
			Context("timeout", timeout).
			Build()
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
func (q *JobQueue) Enqueue(ctx context.Context, action Action, data any, config RetryConfig) (*Job, error) {
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
		if !q._dropOldestPendingJob(ctx) {
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

			return nil, errors.New(ErrQueueFull).
				Context("operation", "enqueue").
				Context("max_jobs", q.maxJobs).
				Context("current_jobs", len(q.jobs)).
				Context("action_type", action.GetDescription()).
				Build()
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
		ID:          shortUUID,
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
func (q *JobQueue) _dropOldestPendingJob(ctx context.Context) bool {
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

	LogJobDropped(ctx, oldestJob.ID, oldestJob.Action.GetDescription())
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
			LogQueueStopped(ctx, "context_cancelled", "error", ctx.Err())
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
			LogQueueStopped(ctx, "stop_channel")
			return
		case <-ctx.Done():
			LogQueueStopped(ctx, "context_done", "error", ctx.Err())
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

// sanitizeErrorMessage returns a sanitized version of the error message
// for safe storage in statistics. This function:
// 1. Handles nil errors
// 2. Scrubs sensitive data (credentials, tokens, emails, IPs) using privacy package
// 3. Bounds the message length to prevent memory bloat
// 4. Removes control characters and potentially unsafe characters
// 5. Handles non-ASCII characters and escape sequences
func sanitizeErrorMessage(err error) string {
	if err == nil {
		return ""
	}

	// First, apply privacy scrubbing to remove sensitive data
	// This handles URLs with credentials, API tokens, emails, IPs, etc.
	errMsg := privacy.ScrubMessage(err.Error())

	// Bound message length to prevent memory bloat
	if len(errMsg) > MaxMessageLength {
		truncatedMsg := errMsg[:MaxMessageLength]
		errMsg = truncatedMsg + "... [truncated]"
	}

	// Enhanced sanitization to handle control characters, escape sequences, and non-ASCII
	errMsg = strings.Map(func(r rune) rune {
		// Remove ASCII control characters
		if r < 32 || r == 127 {
			return -1
		}

		// Remove potentially problematic Unicode characters
		if r >= 0xFFF0 && r <= 0xFFFF { // Unicode specials
			return -1
		}

		return r
	}, errMsg)

	// Replace common escape sequences that might have survived
	replacer := strings.NewReplacer(
		"\\n", " ",
		"\\r", " ",
		"\\t", " ",
		"\\\"", "\"",
		"\\\\", "\\",
	)
	errMsg = replacer.Replace(errMsg)

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
		LogJobRetrying(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts)
	}

	// Create a timeout context for the job execution
	execCtx, cancel := context.WithTimeout(ctx, DefaultJobExecutionTimeout)
	defer cancel()

	// Execute the job with proper context handling and error capture
	type result struct {
		err error
	}
	resultChan := make(chan result, 1)

	go func() {
		// Add panic recovery to prevent goroutine crashes
		defer func() {
			if r := recover(); r != nil {
				// Convert panic to error
				panicErr := errors.Newf("job execution panicked: %v", r).
					Component("analysis.jobqueue").
					Category(errors.CategoryProcessing).
					Context("operation", "execute_job").
					Context("job_id", job.ID).
					Context("action_type", fmt.Sprintf("%T", job.Action)).
					Context("panic_value", fmt.Sprintf("%v", r)).
					Build()
				resultChan <- result{err: panicErr}
			}
		}()

		err := job.Action.Execute(job.Data)
		resultChan <- result{err: err}
	}()

	// Wait for completion, timeout, or cancellation
	var err error
	select {
	case res := <-resultChan:
		// Normal completion
		err = res.err
	case <-execCtx.Done():
		// Context timeout or cancellation
		ctxErr := execCtx.Err()
		if ctxErr == context.DeadlineExceeded {
			err = errors.New(ctxErr).
				Component("analysis.jobqueue").
				Category(errors.CategoryTimeout).
				Context("operation", "execute_job").
				Context("job_id", job.ID).
				Context("action_type", fmt.Sprintf("%T", job.Action)).
				Context("timeout", DefaultJobExecutionTimeout.String()).
				Context("attempt", job.Attempts).
				Build()
		} else {
			// Context cancelled - preserve "cancelled" text for test compatibility
			err = errors.Newf("job execution cancelled: %w", ctxErr).
				Component("analysis.jobqueue").
				Category(errors.CategoryCancellation).
				Context("operation", "execute_job").
				Context("job_id", job.ID).
				Context("action_type", fmt.Sprintf("%T", job.Action)).
				Context("attempt", job.Attempts).
				Build()
		}
	}

	// Calculate execution duration
	executionEndTime := q.clock.Now()
	executionDuration := executionEndTime.Sub(executionStartTime)

	// Handle the result
	q.mu.Lock()
	defer q.mu.Unlock()

	// Check if we need to clean up old action stats to prevent memory growth
	if len(q.stats.ActionStats) >= MaxActionStatsEntries {
		q.cleanupOldActionStats()
	}

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

			LogJobFailed(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts, err)
		} else {
			// Schedule for retry
			job.Status = JobStatusRetrying

			// Calculate backoff with exponential strategy
			delay := calculateBackoffDelay(job.Config, job.Attempts, q.clock)
			job.NextRetryAt = q.clock.Now().Add(delay)

			// Log detailed retry scheduling information
			LogJobRetryScheduled(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts, delay, job.NextRetryAt, err)
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
			LogJobSuccess(ctx, job.ID, actionDesc, job.Attempts)
		}
	}
}

// cleanupOldActionStats removes the oldest action stats entries to prevent unbounded memory growth
// This method must be called with q.mu locked
func (q *JobQueue) cleanupOldActionStats() {
	// Find the oldest entries by last execution time
	type statEntry struct {
		key  string
		time time.Time
	}

	entries := make([]statEntry, 0, len(q.stats.ActionStats))
	for key := range q.stats.ActionStats {
		stat := q.stats.ActionStats[key]
		entries = append(entries, statEntry{
			key:  key,
			time: stat.LastExecutionTime,
		})
	}

	// Sort by time (oldest first) using standard library
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].time.Before(entries[j].time)
	})

	// Calculate exact number to remove to reach target size with hysteresis margin
	currentSize := len(entries)
	toRemove := currentSize - ActionStatsTargetSize
	if toRemove <= 0 {
		toRemove = 1 // Always remove at least one entry to prevent repeated triggers
	}

	for i := 0; i < toRemove && i < len(entries); i++ {
		delete(q.stats.ActionStats, entries[i].key)
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
		PendingJobs:  len(q.jobs),
		MaxQueueSize: q.maxJobs,
		QueueUtilization: func() float64 {
			if q.maxJobs == 0 { // Avoid division by zero
				return 0
			}
			return float64(len(q.jobs)) / float64(q.maxJobs) * 100.0
		}(),

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

// EnqueueTypedWithContext adds a typed job to the queue with context support
func (q *TypedJobQueue[T]) EnqueueTypedWithContext(ctx context.Context, action TypedAction[T], data T, config RetryConfig) (*TypedJob[T], error) {
	// Create an adapter that converts the typed action to a regular action
	adapter := &typedActionAdapter[T]{
		action: action,
		data:   data,
	}

	// Enqueue the job using the adapter with provided context
	job, err := q.Enqueue(ctx, adapter, nil, config)
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

// EnqueueTyped adds a typed job to the queue (backward compatibility)
func (q *TypedJobQueue[T]) EnqueueTyped(action TypedAction[T], data T, config RetryConfig) (*TypedJob[T], error) {
	return q.EnqueueTypedWithContext(context.Background(), action, data, config)
}

// typedActionAdapter adapts a TypedAction to the Action interface
type typedActionAdapter[T any] struct {
	action TypedAction[T]
	data   T
}

// Execute implements the Action interface
func (a *typedActionAdapter[T]) Execute(data any) error {
	// If data is provided, ensure it's the correct type and use it
	if data != nil {
		if typedData, ok := data.(T); ok {
			return a.action.Execute(typedData)
		}
		return errors.Newf("invalid data type: expected %T, got %T", a.data, data).
			Component("analysis.jobqueue").
			Category(errors.CategoryValidation).
			Context("operation", "execute_typed_action").
			Context("expected_type", fmt.Sprintf("%T", a.data)).
			Context("actual_type", fmt.Sprintf("%T", data)).
			Build()
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
