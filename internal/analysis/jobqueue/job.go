package jobqueue

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"
)

// Job represents a unit of work in the job queue
type Job struct {
	ID                     string      // Unique ID for this job
	Action                 Action      // The action to execute
	Data                   any         // Data for the action
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
	Execute(ctx context.Context, data T) error
	GetDescription() string // Returns a human-readable description of the action
}

// ToJSON converts the JobStatsSnapshot to a JSON string with pretty formatting
func (s *JobStatsSnapshot) ToJSON() (string, error) {
	return s.toJSON(true)
}

// ToJSONCompact converts the JobStatsSnapshot to a compact JSON string
// This is more efficient for production use where pretty formatting isn't needed
func (s *JobStatsSnapshot) ToJSONCompact() (string, error) {
	return s.toJSON(false)
}

// toJSON is the internal implementation that handles both pretty and compact JSON
func (s *JobStatsSnapshot) toJSON(prettyPrint bool) (string, error) {
	// Use a single timestamp for consistency across the entire snapshot
	snapshotTime := time.Now()
	formattedTime := snapshotTime.Format(time.RFC3339)

	// Create a map to represent the JSON structure
	statsMap := map[string]any{
		"queue": map[string]any{
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
		"actions":   make(map[string]any),
		"timestamp": formattedTime,
	}
	// Pre-allocate actions map with the right capacity
	actionsMap := make(map[string]any, len(s.ActionStats))
	statsMap["actions"] = actionsMap

	// Add action stats to the map
	for typeName := range s.ActionStats {
		// Get a reference to the stats to avoid copying the large struct
		stats := s.ActionStats[typeName]

		// Bound description length to prevent bloat in JSON output (use rune count for UTF-8 safety)
		description := stats.Description
		if utf8.RuneCountInString(description) > MaxMessageLength {
			runes := []rune(description)
			description = string(runes[:MaxMessageLength]) + "... [truncated]"
		}

		// Create action stats map with metrics and performance data
		actionStats := map[string]any{
			"typeName":    stats.TypeName,
			"description": description,
			"metrics": map[string]any{
				"attempted":  stats.Attempted,
				"successful": stats.Successful,
				"failed":     stats.Failed,
				"retried":    stats.Retried,
				"dropped":    stats.Dropped,
			},
			"performance": map[string]any{
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
		actionsMap[typeName] = actionStats
	}

	// Convert to JSON based on format preference
	var jsonBytes []byte
	var err error

	if prettyPrint {
		jsonBytes, err = json.MarshalIndent(statsMap, "", "  ")
	} else {
		jsonBytes, err = json.Marshal(statsMap)
	}

	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}
