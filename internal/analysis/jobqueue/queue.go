package jobqueue

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	stopCh             chan struct{}
	runningJobs        sync.WaitGroup // Track running jobs for graceful shutdown
	isRunning          bool
	maxArchivedJobs    int  // Maximum number of archived jobs to keep
	maxJobs            int  // Maximum number of pending jobs in the queue
	droppedJobs        int  // Counter for jobs dropped due to queue full
	logAllSuccesses    bool // Whether to log all successful jobs, not just retries
	allowJobDropping   bool // Whether dropping oldest job is allowed when queue is full
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
		allowJobDropping:   true, // Default to allowing job dropping when queue is full
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

// SetAllowJobDropping sets whether jobs can be dropped when queue is full.
// Primarily used for testing scenarios.
func (q *JobQueue) SetAllowJobDropping(allow bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.allowJobDropping = allow
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
		if !q.dropOldestPendingJob(ctx) {
			// Could not drop any job, queue is full
			q.droppedJobs++
			q.stats.DroppedJobs++

			// Update action-specific stats for dropped job
			actionKey, stats := q.getOrInitActionStatsLocked(action, action.GetDescription())
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
	actionKey, stats := q.getOrInitActionStatsLocked(action, action.GetDescription())
	stats.Attempted++
	q.stats.ActionStats[actionKey] = stats

	return job, nil
}

// dropOldestPendingJob removes the oldest pending job from the queue
// to make room for a new job. Returns true if a job was dropped.
//
// Performance: O(N) scan through jobs. Acceptable for default maxJobs=1000.
// If maxJobs is set significantly higher and queue-full scenarios are common,
// consider using a min-heap ordered by CreatedAt for O(log N) removal.
//
// IMPORTANT: This method must be called with q.mu already locked.
func (q *JobQueue) dropOldestPendingJob(ctx context.Context) bool {
	// For testing queue overflow, respect the allowJobDropping setting
	if !q.allowJobDropping {
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
	actionKey, stats := q.getOrInitActionStatsLocked(oldestJob.Action, oldestJob.Action.GetDescription())
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

// revertRunningJobsToPending reverts all running jobs back to pending/retrying status.
// Called when context is cancelled during job processing.
func (q *JobQueue) revertRunningJobsToPending(jobs []*Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range jobs {
		if j.Status == JobStatusRunning {
			if j.Attempts > 0 {
				j.Status = JobStatusRetrying
			} else {
				j.Status = JobStatusPending
			}
		}
	}
}

// processDueJobs processes jobs that are due for execution
func (q *JobQueue) processDueJobs(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	q.mu.Lock()
	var dueJobs []*Job
	now := q.clock.Now()

	for _, job := range q.jobs {
		if (job.Status == JobStatusPending || job.Status == JobStatusRetrying) && !job.NextRetryAt.After(now) {
			dueJobs = append(dueJobs, job)
			job.Status = JobStatusRunning
		}
	}

	// Add to WaitGroup while holding lock to prevent shutdown race.
	// This ensures StopWithTimeout sees the correct count before Wait().
	if len(dueJobs) > 0 {
		q.runningJobs.Add(len(dueJobs))
	}
	q.mu.Unlock()

	for i, job := range dueJobs {
		if ctx.Err() != nil {
			// Revert unspawned jobs and adjust WaitGroup count
			unspawnedCount := len(dueJobs) - i
			q.revertRunningJobsToPending(dueJobs[i:])
			q.runningJobs.Add(-unspawnedCount) // Decrement for jobs we won't spawn
			return
		}

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

	// Bound message length to prevent memory bloat (use rune count for UTF-8 safety)
	if utf8.RuneCountInString(errMsg) > MaxMessageLength {
		runes := []rune(errMsg)
		errMsg = string(runes[:MaxMessageLength]) + "... [truncated]"
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

// getOrInitActionStatsLocked returns existing action stats or initializes new ones.
// IMPORTANT: Caller must hold q.mu lock.
// Takes description as argument to avoid redundant GetDescription() calls.
func (q *JobQueue) getOrInitActionStatsLocked(action Action, description string) (string, ActionStats) {
	actionKey := getActionKey(action)
	stats, exists := q.stats.ActionStats[actionKey]
	if !exists {
		stats = ActionStats{
			TypeName:    fmt.Sprintf("%T", action),
			Description: description,
		}
	}
	return actionKey, stats
}

// handleExecutionTimeout creates an appropriate error for timeout/cancellation.
func handleExecutionTimeout(ctxErr error, job *Job) error {
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return errors.New(ctxErr).
			Component("analysis.jobqueue").
			Category(errors.CategoryTimeout).
			Context("operation", "execute_job").
			Context("job_id", job.ID).
			Context("action_type", fmt.Sprintf("%T", job.Action)).
			Context("timeout", DefaultJobExecutionTimeout.String()).
			Context("attempt", job.Attempts).
			Build()
	}
	return errors.Newf("job execution cancelled: %w", ctxErr).
		Component("analysis.jobqueue").
		Category(errors.CategoryCancellation).
		Context("operation", "execute_job").
		Context("job_id", job.ID).
		Context("action_type", fmt.Sprintf("%T", job.Action)).
		Context("attempt", job.Attempts).
		Build()
}

// updateDurationStats updates min/max/average duration statistics.
func (stats *ActionStats) updateDurationStats(duration time.Duration) {
	stats.TotalDuration += duration
	if stats.MinDuration == 0 || duration < stats.MinDuration {
		stats.MinDuration = duration
	}
	if duration > stats.MaxDuration {
		stats.MaxDuration = duration
	}
	totalAttempts := stats.Successful + stats.Failed + stats.Retried
	if totalAttempts > 0 {
		stats.AverageDuration = time.Duration(int64(stats.TotalDuration) / int64(totalAttempts))
	}
}

// executeJob executes a job and handles retries if needed
func (q *JobQueue) executeJob(ctx context.Context, job *Job) {
	// Get action description for logging (safe to call without lock)
	actionDesc := job.Action.GetDescription()

	// Update stats at start - increment attempt counter under lock
	q.mu.Lock()
	job.Attempts++ // Protected by mutex to avoid race with revertRunningJobsToPending
	if job.Attempts > 1 {
		q.stats.RetryAttempts++
	}
	actionKey, stats := q.getOrInitActionStatsLocked(job.Action, actionDesc)
	stats.Attempted++
	if job.Attempts > 1 {
		stats.Retried++
	}
	executionStartTime := q.clock.Now()
	stats.LastExecutionTime = executionStartTime
	q.stats.ActionStats[actionKey] = stats
	q.mu.Unlock()

	if job.Attempts > 1 {
		LogJobRetrying(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts)
	}

	// Execute with timeout
	err := q.executeJobWithTimeout(ctx, job)

	// Calculate execution duration
	executionEndTime := q.clock.Now()
	executionDuration := executionEndTime.Sub(executionStartTime)

	// Handle the result
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.stats.ActionStats) >= MaxActionStatsEntries {
		q.cleanupOldActionStats()
	}

	stats = q.stats.ActionStats[actionKey]
	stats.updateDurationStats(executionDuration)

	if err != nil {
		q.handleJobFailure(ctx, job, &stats, actionKey, actionDesc, executionEndTime, err)
	} else {
		q.handleJobSuccess(ctx, job, &stats, actionKey, actionDesc, executionEndTime)
	}
}

// executeJobWithTimeout runs the job action with timeout and panic recovery.
//
// IMPORTANT: Action implementations MUST respect context cancellation by checking
// ctx.Done() periodically, especially in long-running operations. If an action
// ignores context cancellation, the goroutine will leak when the timeout expires
// because Go does not support forcibly terminating goroutines.
func (q *JobQueue) executeJobWithTimeout(ctx context.Context, job *Job) error {
	execCtx, cancel := context.WithTimeout(ctx, DefaultJobExecutionTimeout)
	defer cancel()

	type result struct {
		err error
	}
	resultChan := make(chan result, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
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

		err := job.Action.Execute(execCtx, job.Data)
		resultChan <- result{err: err}
	}()

	select {
	case res := <-resultChan:
		return res.err
	case <-execCtx.Done():
		return handleExecutionTimeout(execCtx.Err(), job)
	}
}

// handleJobFailure processes a failed job execution.
// IMPORTANT: Caller must hold q.mu lock.
func (q *JobQueue) handleJobFailure(ctx context.Context, job *Job, stats *ActionStats, actionKey, actionDesc string, endTime time.Time, err error) {
	job.LastError = err
	stats.LastErrorMessage = sanitizeErrorMessage(err)
	stats.LastFailedTime = endTime

	if job.Attempts >= job.MaxAttempts {
		job.Status = JobStatusFailed
		q.stats.FailedJobs++
		stats.Failed++
		q.stats.ActionStats[actionKey] = *stats
		LogJobFailed(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts, err)
	} else {
		job.Status = JobStatusRetrying
		delay := calculateBackoffDelay(job.Config, job.Attempts, q.clock)
		job.NextRetryAt = q.clock.Now().Add(delay)
		q.stats.ActionStats[actionKey] = *stats
		LogJobRetryScheduled(ctx, job.ID, actionDesc, job.Attempts, job.MaxAttempts, delay, job.NextRetryAt, err)
	}
}

// handleJobSuccess processes a successful job execution.
// IMPORTANT: Caller must hold q.mu lock.
func (q *JobQueue) handleJobSuccess(ctx context.Context, job *Job, stats *ActionStats, actionKey, actionDesc string, endTime time.Time) {
	job.Status = JobStatusCompleted
	stats.LastSuccessfulTime = endTime
	q.stats.SuccessfulJobs++
	stats.Successful++
	q.stats.ActionStats[actionKey] = *stats

	if job.Attempts > 1 || q.logAllSuccesses {
		LogJobSuccess(ctx, job.ID, actionDesc, job.Attempts)
	}
}

// extractTypeNameFromKey extracts the type name from an action key.
// Key format is "TypeName:Description" where Description may contain escaped colons.
func extractTypeNameFromKey(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' && (i == 0 || key[i-1] != '\\') {
			return key[:i]
		}
	}
	return key // Fallback if no unescaped colon found
}

// updateAggregatedDurations updates min/max durations during aggregation.
func updateAggregatedDurations(aggregated, s *ActionStats) {
	if s.MinDuration > 0 && (aggregated.MinDuration == 0 || s.MinDuration < aggregated.MinDuration) {
		aggregated.MinDuration = s.MinDuration
	}
	if s.MaxDuration > aggregated.MaxDuration {
		aggregated.MaxDuration = s.MaxDuration
	}
}

// updateAggregatedTimestamps updates timestamps during aggregation.
func updateAggregatedTimestamps(aggregated, s *ActionStats) {
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

// recalculateAverageDuration recalculates average from totals.
func recalculateAverageDuration(stats *ActionStats) {
	totalAttempts := stats.Successful + stats.Failed + stats.Retried
	if totalAttempts > 0 {
		stats.AverageDuration = time.Duration(int64(stats.TotalDuration) / int64(totalAttempts))
	}
}

// aggregateActionStats combines multiple ActionStats into one.
func aggregateActionStats(statsList []ActionStats) ActionStats {
	if len(statsList) == 0 {
		return ActionStats{}
	}

	aggregated := statsList[0]
	for i := 1; i < len(statsList); i++ {
		s := &statsList[i]
		aggregated.Attempted += s.Attempted
		aggregated.Successful += s.Successful
		aggregated.Failed += s.Failed
		aggregated.Retried += s.Retried
		aggregated.Dropped += s.Dropped
		aggregated.TotalDuration += s.TotalDuration

		updateAggregatedDurations(&aggregated, s)
		updateAggregatedTimestamps(&aggregated, s)
	}

	recalculateAverageDuration(&aggregated)
	return aggregated
}

// buildStatsSnapshotMapsLocked builds both the action stats copy and the type grouping map.
// Returns (actionStatsCopy, typeNameMap) to avoid iterating twice.
// IMPORTANT: Caller must hold q.mu lock.
func (q *JobQueue) buildStatsSnapshotMapsLocked() (actionStatsCopy map[string]ActionStats, typeNameMap map[string][]ActionStats) {
	actionStatsCopy = make(map[string]ActionStats, len(q.stats.ActionStats))
	typeNameMap = make(map[string][]ActionStats)

	for k := range q.stats.ActionStats {
		v := q.stats.ActionStats[k]
		typeName := extractTypeNameFromKey(k)

		// Update description from active jobs if available
		for _, job := range q.jobs {
			if getActionKey(job.Action) == k && job.Action != nil {
				v.Description = job.Action.GetDescription()
				break
			}
		}

		actionStatsCopy[k] = v
		typeNameMap[typeName] = append(typeNameMap[typeName], v)
	}
	return actionStatsCopy, typeNameMap
}

// cleanupOldActionStats removes the oldest action stats entries to prevent unbounded memory growth.
//
// Note: If a cleaned-up action type later retries, its stats will reinitialize from zero.
// This is acceptable as it only affects long-inactive action types, and the alternative
// (never cleaning up) risks unbounded memory growth.
//
// This method must be called with q.mu locked.
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

	// Build both maps in a single pass
	actionStatsCopy, typeNameMap := q.buildStatsSnapshotMapsLocked()

	// Add aggregated stats by type name for backward compatibility
	for typeName, statsList := range typeNameMap {
		if len(statsList) > 0 {
			actionStatsCopy[typeName] = aggregateActionStats(statsList)
		}
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
	*JobQueue // Pointer embedding avoids copying sync primitives
}

// NewTypedJobQueue creates a new typed job queue
func NewTypedJobQueue[T any]() *TypedJobQueue[T] {
	return &TypedJobQueue[T]{
		JobQueue: NewJobQueue(),
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
func (a *typedActionAdapter[T]) Execute(ctx context.Context, data any) error {
	// If data is provided, ensure it's the correct type and use it
	if data != nil {
		if typedData, ok := data.(T); ok {
			return a.action.Execute(ctx, typedData)
		}
		return errors.Newf("invalid data type: expected %T, got %T", a.data, data).
			Component("analysis.jobqueue").
			Category(errors.CategoryValidation).
			Context("operation", "execute_typed_action").
			Context("expected_type", fmt.Sprintf("%T", a.data)).
			Context("actual_type", fmt.Sprintf("%T", data)).
			Build()
	}
	return a.action.Execute(ctx, a.data)
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
