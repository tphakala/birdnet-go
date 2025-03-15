# JobQueue

A specialized job queue implementation for BirdNET-Go that manages and processes bird species detection actions asynchronously. This module handles the execution of various post-detection tasks such as submitting findings to BirdWeather, sending MQTT notifications, writing to database, and other configurable actions triggered by bird species identifications.

## Overview

The `jobqueue` package provides a job queue implementation with the following key features:

- **Configurable Retry Policies**: Exponential backoff with jitter for failed jobs
- **Rate Limiting**: Control the maximum number of jobs in the queue
- **Job Prioritization**: Ability to drop oldest jobs when the queue is full
- **Graceful Shutdown**: Wait for in-progress jobs to complete
- **Panic Recovery**: Automatically recover from panics in job execution
- **Comprehensive Statistics**: Track success, failure, and retry metrics
- **Type-Safe API**: Generic implementation for type safety
- **Context Support**: Cancel jobs via context cancellation
- **Timeout Handling**: Automatically timeout hanging jobs

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
- **Action**: Interface that defines the executable work
- **RetryConfig**: Configuration for retry behavior
- **JobStatus**: Enum representing the current status of a job

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

func (a *MyAction) Execute(data interface{}) error {
    // Process the data
    myData := data.(MyDataType)
    // Do something with myData
    return nil
}

// Enqueue a job
action := &MyAction{}
data := MyDataType{...}
config := jobqueue.GetDefaultRetryConfig(true)
job, err := queue.Enqueue(action, data, config)
if err != nil {
    log.Fatalf("Failed to enqueue job: %v", err)
}
```

### Type-Safe Usage

```go
// Create a typed job queue
queue := jobqueue.NewTypedJobQueue[MyDataType]()
queue.Start()
defer queue.Stop()

// Define a typed action
type MyTypedAction struct{}

func (a *MyTypedAction) Execute(data MyDataType) error {
    // Process the data
    // No type assertion needed
    return nil
}

// Enqueue a typed job
action := &MyTypedAction{}
data := MyDataType{...}
config := jobqueue.GetDefaultRetryConfig(true)
job, err := queue.EnqueueTyped(action, data, config)
if err != nil {
    log.Fatalf("Failed to enqueue job: %v", err)
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

### Queue Configuration

1. **Queue Size**: Set appropriate queue size limits based on memory constraints
2. **Retry Policy**: Configure retry policies based on the nature of the work
3. **Archive Limits**: Set appropriate archive limits to prevent memory leaks
4. **Processing Interval**: Adjust the processing interval based on workload characteristics

### Monitoring

1. **Track Statistics**: Regularly monitor queue statistics to detect issues
2. **Log Analysis**: Analyze logs for patterns of failures
3. **Resource Usage**: Monitor memory and CPU usage during high load

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

func (a *MyCustomAction) Execute(data interface{}) error {
    // Custom implementation
    return nil
}
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

The queue tracks statistics per action type:

```go
stats := queue.GetStats()
actionType := fmt.Sprintf("%T", myAction)
actionStats := stats.ActionStats[actionType]
fmt.Printf("Attempted: %d\n", actionStats.Attempted)
fmt.Printf("Successful: %d\n", actionStats.Successful)
fmt.Printf("Failed: %d\n", actionStats.Failed)
fmt.Printf("Retried: %d\n", actionStats.Retried)
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

## License

This package is part of the BirdNet-Go project and is subject to its licensing terms. 