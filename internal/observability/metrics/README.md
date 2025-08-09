# Metrics Package

This package provides Prometheus metrics for the BirdNET-Go application, including a `Recorder` interface for improved testability.

## Recorder Interface

The `Recorder` interface provides a minimal abstraction for recording metrics, improving testability and reducing coupling between components and specific metric implementations.

### Interface Definition

```go
type Recorder interface {
    RecordOperation(operation, status string)
    RecordDuration(operation string, seconds float64)
    RecordError(operation, errorType string)
}
```

### Benefits

1. **Improved Testability**: Use `TestRecorder` or `NoOpRecorder` in tests instead of real metrics
2. **Reduced Coupling**: Components depend on the interface, not concrete implementations
3. **Flexibility**: Easy to swap metric backends or add new implementations
4. **Simplicity**: Minimal interface covers most metric recording needs

### Available Implementations

#### Production Implementations

- `BirdNETMetrics` - For BirdNET model operations
- `DatastoreMetrics` - For database operations
- Other concrete metric types implement this interface

#### Test Implementations

- `TestRecorder` - Captures metrics for verification in tests (includes `HasRecordedMetrics()` for negative tests)
- `NoOpRecorder` - Does nothing, useful when metrics aren't needed

## Operation Naming Conventions

To ensure consistency across the codebase, follow these naming conventions for operations:

### General Patterns

- Use lowercase with underscores: `operation_name`
- Be specific but concise: `db_query` not `database_query_operation`
- Include the action and target: `cache_get`, `model_load`

### Common Operation Names

#### Database Operations

- `db_query` - Database query operations
- `db_insert` - Database insert operations
- `db_update` - Database update operations
- `db_delete` - Database delete operations
- `transaction` - Database transaction operations

**Note**: For DatastoreMetrics, database operations should include the table name using the format `operation:table` (e.g., `db_query:notes`, `db_insert:detections`). This allows proper tracking of per-table metrics.

#### Model Operations

- `model_load` - Loading ML models
- `prediction` - Running predictions
- `chunk_process` - Processing audio chunks
- `model_invoke` - Direct model invocation
- `range_filter` - Applying range filters

#### Cache Operations

- `cache_get` - Retrieving from cache
- `cache_set` - Writing to cache
- `cache_delete` - Removing from cache
- `cache_hit` - Successful cache retrieval
- `cache_miss` - Failed cache retrieval

#### Note Operations

- `note_create` - Creating new notes
- `note_update` - Updating existing notes
- `note_delete` - Deleting notes
- `note_get` - Retrieving notes
- `note_lock` - Acquiring note locks

#### Other Operations

- `search` - Search operations
- `analytics` - Analytics queries
- `weather_data` - Weather data operations
- `image_cache` - Image caching operations
- `backup` - Backup operations
- `maintenance` - Maintenance operations

### Status Values

- `success` - Operation completed successfully
- `error` - Operation failed with an error
- `started` - Operation has begun (for long-running operations)
- `completed` - Operation finished (alternative to success)
- `timeout` - Operation timed out
- `cancelled` - Operation was cancelled

### Error Types

- `validation` - Input validation errors
- `io` - I/O related errors
- `network` - Network communication errors
- `timeout` - Timeout errors
- `permission` - Permission/authorization errors
- `not_found` - Resource not found errors
- `conflict` - Conflict errors (e.g., concurrent modification)
- `model_error` - ML model specific errors
- `connection` - Connection errors

## Usage Examples

### Component with Metrics

```go
type MyService struct {
    metrics metrics.Recorder
}

func NewMyService(recorder metrics.Recorder) *MyService {
    return &MyService{metrics: recorder}
}

func (s *MyService) ProcessData() error {
    start := time.Now()
    defer func() {
        s.metrics.RecordDuration("process_data", time.Since(start).Seconds())
    }()

    // Do processing...

    s.metrics.RecordOperation("process_data", "success")
    return nil
}
```

### Testing with TestRecorder

```go
func TestProcessData(t *testing.T) {
    recorder := metrics.NewTestRecorder()
    service := NewMyService(recorder)

    err := service.ProcessData()
    if err != nil {
        t.Fatal(err)
    }

    // Verify metrics
    if count := recorder.GetOperationCount("process_data", "success"); count != 1 {
        t.Errorf("expected 1 success operation, got %d", count)
    }

    // Check if any metrics were recorded
    if !recorder.HasRecordedMetrics() {
        t.Error("expected metrics to be recorded")
    }
}

func TestNoMetricsRecorded(t *testing.T) {
    recorder := metrics.NewTestRecorder()

    // Verify no metrics were recorded
    if recorder.HasRecordedMetrics() {
        t.Error("expected no metrics to be recorded")
    }
}
```

### Migration from Concrete Types

Before:

```go
type Service struct {
    metrics *metrics.DatastoreMetrics
}
```

After:

```go
type Service struct {
    metrics metrics.Recorder
}
```

## Best Practices

1. **Use descriptive operation names**: Follow the naming conventions above
2. **Record both success and failure**: Always record operation status
3. **Measure critical paths**: Focus on operations that impact performance
4. **Keep it simple**: The interface is minimal by design - use it for common cases
5. **Be consistent**: Use the same operation names across similar components

## When to Use Concrete Types vs Interface

Use the **Recorder interface** when:

- The component needs basic metric recording (operations, durations, errors)
- You want to improve testability
- The component might be reused with different metric backends

Use **concrete metric types** when:

- You need access to specialized metrics (e.g., histograms, gauges)
- The component is tightly coupled to a specific metric implementation
- You need the full Prometheus collector interface
