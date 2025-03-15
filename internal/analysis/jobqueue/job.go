package jobqueue

import (
	"time"
)

// Job represents a unit of work in the job queue
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
	TotalJobs      int
	SuccessfulJobs int
	FailedJobs     int
	StaleJobs      int
	ArchivedJobs   int
	DroppedJobs    int
	RetryAttempts  int
	ActionStats    map[string]ActionStats // Key is the type name of the action
}

// ActionStats tracks statistics for a specific action type
type ActionStats struct {
	Attempted  int
	Successful int
	Failed     int
	Retried    int
	Dropped    int // Track number of jobs dropped due to queue full
}

// TypedJob is a generic version of Job for type-safe operations
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

// TypedAction is a generic version of Action for type-safe operations
type TypedAction[T any] interface {
	Execute(data T) error
}
