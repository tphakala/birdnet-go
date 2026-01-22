# JobQueue

A specialized job queue implementation for BirdNET-Go that manages and processes bird species detection actions asynchronously. This module handles the execution of various post-detection tasks such as submitting findings to BirdWeather, sending MQTT notifications, writing to database, and other configurable actions triggered by bird species identifications.

## Overview

The `jobqueue` package provides a job queue implementation with the following key features:

- **Configurable Retry Policies**: Exponential backoff with jitter for failed jobs
- **Rate Limiting**: Control the maximum number of jobs in the queue
- **Job Prioritization**: Ability to drop oldest jobs when the queue is full
- **Graceful Shutdown**: Wait for in-progress jobs to complete
- **Panic Recovery**: Automatically recover from panics in job execution
- **Comprehensive Statistics**: Track success, failure, and retry metrics per action type
- **Type-Safe API**: Generic implementation for type safety
- **Context Support**: Cancel jobs via context cancellation
- **Timeout Handling**: Automatically timeout hanging jobs
- **Action Description Tracking**: Track statistics based on both action type and description
- **Unique Job IDs**: Uses UUID v4 (truncated to 12 characters) for job identifiers

## Job IDs

Jobs are assigned unique identifiers using the format `job-xxxxxxx` where:

- The prefix "job-" identifies it as a job identifier
- The "xxxxxxx" part is a truncated UUID v4 (first 12 characters)
- This provides reliable uniqueness while keeping IDs reasonably short
- Example: `job-1a2b3c4d5e6f`

## Integration with Processor

The `jobqueue` package is integrated directly with the `processor` package through the `Processor.EnqueueTask` method. Each processor instance maintains its own job queue, eliminating the need for global state or initialization functions.

Tasks are enqueued directly to the processor's job queue using:

```go
// Enqueue a task to the processor's job queue
err := processor.EnqueueTask(task)
```

The `ActionAdapter` in the processor package adapts the processor-specific `Action` interface to the jobqueue's `Action` interface, allowing processor actions to be executed by the job queue.

## Architecture

The job queue is designed around these core components:

### Core Types

- **JobQueue**: The main queue that manages jobs and their lifecycle
- **Job**: Represents a unit of work with its metadata and status
- **Action**: Interface that defines the executable work and its description
- **RetryConfig**: Configuration for retry behavior
- **JobStatus**: Enum representing the current status of a job
- **JobStats**: Tracks statistics about job processing
- **ActionStats**: Tracks statistics for a specific action type

### Job Lifecycle

1. **Enqueue**: Jobs are added to the queue with an action, data, and retry configuration
2. **Process**: The queue processes jobs at regular intervals
3. **Execute**: Jobs are executed in separate goroutines
4. **Retry/Complete**: Based on execution results, jobs are either marked as completed, scheduled for retry, or marked as failed

## Usage Examples

### Basic Usage

```go
// Create a new job queue with default settings
queue := jobqueue.NewJobQueue()
queue.Start()
defer queue.Stop()

// Define an action
type MyAction struct{}

func (a *MyAction) Execute(ctx context.Context, data interface{}) error {
    // Process the data (check ctx.Done() for cancellation in long operations)
    myData := data.(MyDataType)
    // Do something with myData
    return nil
}

func (a *MyAction) GetDescription() string {
    return "My custom action description"
}

// Enqueue a job
action := &MyAction{}
data := MyDataType{...}
config := jobqueue.GetDefaultRetryConfig(true)
job, err := queue.Enqueue(action, data, config)
if err != nil {
    return fmt.Errorf("failed to enqueue job: %w", err)
}

// Access the UUID-based job ID
fmt.Printf("Job ID: %s\n", job.ID)  // e.g., "job-1a2b3c4d5e6f"
```

### Type-Safe Usage

```go
// Create a typed job queue
queue := jobqueue.NewTypedJobQueue[MyDataType]()
queue.Start()
defer queue.Stop()

// Define a typed action
type MyTypedAction struct{}

func (a *MyTypedAction) Execute(ctx context.Context, data MyDataType) error {
    // Process the data (check ctx.Done() for cancellation in long operations)
    // No type assertion needed
    return nil
}

func (a *MyTypedAction) GetDescription() string {
    return "My typed action description"
}

// Enqueue a typed job
action := &MyTypedAction{}
data := MyDataType{...}
config := jobqueue.GetDefaultRetryConfig(true)
job, err := queue.EnqueueTyped(action, data, config)
if err != nil {
    return fmt.Errorf("failed to enqueue job: %w", err)
}
```

### Custom Retry Configuration

```go
// Create a custom retry configuration
config := jobqueue.RetryConfig{
    Enabled:      true,
    MaxRetries:   3,
    InitialDelay: 5 * time.Second,
    MaxDelay:     1 * time.Minute,
    Multiplier:   2.0,
}

// Enqueue a job with the custom retry configuration
job, err := queue.Enqueue(action, data, config)
```

### Handling Job Cancellation

```go
// Create a context that can be cancelled
ctx, cancel := context.WithCancel(context.Background())

// Start the queue with the context
queue := jobqueue.NewJobQueue()
queue.StartWithContext(ctx)

// Enqueue jobs...

// Cancel all jobs
cancel()

// Wait for graceful shutdown
queue.Stop()
```

## Best Practices

### Job Design

1. **Idempotency**: Design jobs to be idempotent so they can be safely retried
2. **Statelessness**: Jobs should not rely on external state that might change between retries
3. **Timeout Awareness**: Jobs should respect context cancellation and timeouts
4. **Error Handling**: Return meaningful errors that help diagnose issues
5. **Descriptive Actions**: Provide meaningful descriptions for actions to aid in monitoring and debugging

### Queue Configuration

1. **Queue Size**: Set appropriate queue size limits based on memory constraints
2. **Retry Policy**: Configure retry policies based on the nature of the work
3. **Archive Limits**: Set appropriate archive limits to prevent memory leaks
4. **Processing Interval**: Adjust the processing interval based on workload characteristics

### Monitoring

1. **Track Statistics**: Regularly monitor queue statistics to detect issues
2. **Log Analysis**: Analyze logs for patterns of failures
3. **Resource Usage**: Monitor memory and CPU usage during high load
4. **API Exposure**: Use the JSON API to monitor queue statistics in real-time

## Implementation Details

### Concurrency Control

The job queue uses a mutex (`sync.Mutex`) to protect shared state and ensure thread safety. Jobs are executed in separate goroutines, allowing for concurrent processing while maintaining control over the number of jobs in the queue.

### Exponential Backoff

Failed jobs are retried with exponential backoff and jitter to prevent thundering herd problems:

```go
backoff := initialDelay * math.Pow(multiplier, attemptNum)
jitterFactor := 0.9 + 0.2*rand
backoff *= jitterFactor
```

### Memory Management

The queue implements several mechanisms to manage memory:

1. **Maximum Queue Size**: Limits the number of pending jobs
2. **Archive Limit**: Limits the number of completed/failed jobs kept in memory
3. **Job Dropping**: Can drop oldest jobs when the queue is full

### Panic Recovery

The queue implements panic recovery to prevent goroutine crashes:

```go
defer func() {
    if r := recover(); r != nil {
        err = fmt.Errorf("job execution panicked: %v", r)
    }
    close(done)
}()
```

## Advanced Features

### Custom Job Types

You can create custom job types by implementing the `Action` interface:

```go
type MyCustomAction struct {
    // Custom fields
}

func (a *MyCustomAction) Execute(ctx context.Context, data interface{}) error {
    // Custom implementation (check ctx.Done() for cancellation in long operations)
    return nil
}

func (a *MyCustomAction) GetDescription() string {
    return "Description of what this action does"
}
```

### Job Identification

Jobs are assigned unique IDs using UUIDs to ensure uniqueness even in high-throughput environments:

```go
// Enqueue a job and get its ID
job, err := queue.Enqueue(action, data, config)
if err != nil {
    return fmt.Errorf("failed to enqueue job: %w", err)
}

// The ID uses the format "job-xxxxxxx" where xxxxxxx is a 12-character UUID v4
// e.g., "job-1a2b3c4d5e6f"

// Jobs can be referenced by ID in logs, making debugging easier
logger.Debug("processing job", logger.String("job_id", job.ID))
```

### Statistics Tracking

The queue tracks comprehensive statistics:

```go
stats := queue.GetStats()
fmt.Printf("Total jobs: %d\n", stats.TotalJobs)
fmt.Printf("Successful jobs: %d\n", stats.SuccessfulJobs)
fmt.Printf("Failed jobs: %d\n", stats.FailedJobs)
fmt.Printf("Retry attempts: %d\n", stats.RetryAttempts)
```

### Action-Specific Statistics

The queue tracks statistics per action type and description:

```go
stats := queue.GetStats()

// Get stats by action type and description (new format)
actionKey := fmt.Sprintf("%T:%s", myAction, myAction.GetDescription())
actionStats := stats.ActionStats[actionKey]

// Get stats by action type only (backward compatibility)
actionType := fmt.Sprintf("%T", myAction)
actionStats := stats.ActionStats[actionType]

fmt.Printf("Attempted: %d\n", actionStats.Attempted)
fmt.Printf("Successful: %d\n", actionStats.Successful)
fmt.Printf("Failed: %d\n", actionStats.Failed)
fmt.Printf("Retried: %d\n", actionStats.Retried)
fmt.Printf("Average Duration: %v\n", actionStats.AverageDuration)
```

### JSON API Integration

The job queue statistics can be exposed through the JSON API:

```go
// Get stats in JSON format
jsonStats, err := stats.ToJSON()
if err != nil {
    return fmt.Errorf("failed to convert stats to JSON: %w", err)
}

// The JSON structure includes:
// - Queue statistics (total, successful, failed, etc.)
// - Action-specific statistics (attempts, successes, failures, etc.)
// - Performance metrics (durations, timestamps, etc.)
```

## Testing

The job queue includes comprehensive tests covering:

1. Basic functionality
2. Retry mechanisms
3. Concurrency handling
4. Panic recovery
5. Rate limiting
6. Memory management
7. Job type statistics
8. Long-running jobs
9. Job cancellation

## For LLM Developers

When working with this codebase, keep in mind:

1. **Thread Safety**: All operations on the queue are thread-safe
2. **Context Propagation**: Contexts are propagated to job execution
3. **Error Wrapping**: Errors are wrapped with context
4. **Type Assertions**: Be careful with type assertions in job execution
5. **Memory Management**: Consider memory implications of large job loads
6. **Panic Recovery**: The queue recovers from panics, but it's better to avoid them
7. **Action Keys**: Action statistics are tracked using a combination of type name and description
8. **Job Identification**: Jobs use UUID v4 (truncated to 12 characters) for uniqueness
9. **Job Tracking**: Job IDs (format: job-xxxxxxx) are used in logs for easier debugging and tracing

## License

This package is part of the BirdNet-Go project and is subject to its licensing terms.
