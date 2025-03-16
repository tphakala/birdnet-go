package jobqueue

import (
	"encoding/json"
	"time"
)

// Job represents a unit of work in the job queue
type Job struct {
	ID                     string      // Unique ID for this job
	Action                 Action      // The action to execute
	Data                   interface{} // Data for the action
	Attempts               int         // Number of attempts made so far
	MaxAttempts            int         // Maximum number of attempts allowed
	CreatedAt              time.Time   // When the job was created
	NextRetryAt            time.Time   // When to next attempt the job
	Status                 JobStatus   // Current status of the job
	LastError              error       // Last error encountered
	Config                 RetryConfig // Retry configuration for this job
	TestExemptFromDropping bool        // Flag to indicate if this job should be exempt from dropping during queue overflow
}

// JobStats tracks statistics about job processing
type JobStats struct {
	TotalJobs      int
	SuccessfulJobs int
	FailedJobs     int
	StaleJobs      int
	ArchivedJobs   int // Track number of archived jobs
	DroppedJobs    int // Track number of jobs dropped due to queue full
	RetryAttempts  int
	ActionStats    map[string]ActionStats // Key is the type name of the action
}

// JobStatsSnapshot provides a point-in-time snapshot of job statistics
type JobStatsSnapshot struct {
	// Queue statistics
	TotalJobs      int
	SuccessfulJobs int
	FailedJobs     int
	StaleJobs      int
	ArchivedJobs   int
	DroppedJobs    int
	RetryAttempts  int

	// Current queue state
	PendingJobs      int     // Current number of jobs in the queue
	MaxQueueSize     int     // Maximum queue capacity
	QueueUtilization float64 // Queue utilization percentage

	// Action-specific statistics
	ActionStats map[string]ActionStats // Key is the type name of the action
}

// ActionStats tracks statistics for a specific action type
type ActionStats struct {
	// Type identifier information
	TypeName    string // The fully qualified type name of the action
	Description string // Human-readable description of this action type

	// Core metrics
	Attempted  int // Total attempts (including retries)
	Successful int // Successfully completed jobs
	Failed     int // Permanently failed jobs (after retry attempts)
	Retried    int // Number of retry attempts
	Dropped    int // Jobs dropped due to queue full

	// Performance metrics
	TotalDuration      time.Duration // Total execution time across all attempts
	AverageDuration    time.Duration // Average execution time per attempt
	MinDuration        time.Duration // Minimum execution time
	MaxDuration        time.Duration // Maximum execution time
	LastExecutionTime  time.Time     // When this action was last executed
	LastSuccessfulTime time.Time     // When this action last succeeded
	LastFailedTime     time.Time     // When this action last failed
	LastErrorMessage   string        // Last error message (sanitized)
}

// TypedJob is a generic version of Job for type-safe operations
type TypedJob[T any] struct {
	ID                     string         // Unique ID for this job
	Action                 TypedAction[T] // The action to execute
	Data                   T              // Data for the action (type-safe)
	Attempts               int            // Number of attempts made so far
	MaxAttempts            int            // Maximum number of attempts allowed
	CreatedAt              time.Time      // When the job was created
	NextRetryAt            time.Time      // When to next attempt the job
	Status                 JobStatus      // Current status of the job
	LastError              error          // Last error encountered
	Config                 RetryConfig    // Retry configuration for this job
	TestExemptFromDropping bool           // Flag to indicate if this job should be exempt from dropping during queue overflow
}

// TypedAction is a generic version of Action for type-safe operations
type TypedAction[T any] interface {
	Execute(data T) error
	GetDescription() string // Returns a human-readable description of the action
}

// ToJSON converts the JobStatsSnapshot to a JSON string
func (s JobStatsSnapshot) ToJSON() (string, error) {
	// Create a map to represent the JSON structure
	statsMap := map[string]interface{}{
		"queue": map[string]interface{}{
			"total":         s.TotalJobs,
			"successful":    s.SuccessfulJobs,
			"failed":        s.FailedJobs,
			"stale":         s.StaleJobs,
			"archived":      s.ArchivedJobs,
			"dropped":       s.DroppedJobs,
			"retryAttempts": s.RetryAttempts,
			"pending":       s.PendingJobs,
			"maxSize":       s.MaxQueueSize,
			"utilization":   s.QueueUtilization,
		},
		"actions":   make(map[string]interface{}),
		"timestamp": time.Now().Format(time.RFC3339),
	}

	// Add action stats to the map
	for typeName, stats := range s.ActionStats {
		actionStats := map[string]interface{}{
			"typeName":    stats.TypeName,
			"description": stats.Description,
			"metrics": map[string]interface{}{
				"attempted":  stats.Attempted,
				"successful": stats.Successful,
				"failed":     stats.Failed,
				"retried":    stats.Retried,
				"dropped":    stats.Dropped,
			},
			"performance": map[string]interface{}{
				"totalDuration":   stats.TotalDuration.String(),
				"averageDuration": stats.AverageDuration.String(),
				"minDuration":     stats.MinDuration.String(),
				"maxDuration":     stats.MaxDuration.String(),
			},
		}

		// Add timestamps if available
		timestamps := make(map[string]string)
		if !stats.LastExecutionTime.IsZero() {
			timestamps["lastExecution"] = stats.LastExecutionTime.Format(time.RFC3339)
		}
		if !stats.LastSuccessfulTime.IsZero() {
			timestamps["lastSuccess"] = stats.LastSuccessfulTime.Format(time.RFC3339)
		}
		if !stats.LastFailedTime.IsZero() {
			timestamps["lastFailure"] = stats.LastFailedTime.Format(time.RFC3339)
		}

		if len(timestamps) > 0 {
			actionStats["timestamps"] = timestamps
		}

		// Add last error if available
		if stats.LastErrorMessage != "" {
			actionStats["lastError"] = stats.LastErrorMessage
		}

		// Add action stats to the actions map
		statsMap["actions"].(map[string]interface{})[typeName] = actionStats
	}

	// Convert to JSON
	jsonBytes, err := json.MarshalIndent(statsMap, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}
