# Recorder Interface Usage Guide

## Overview

The `Recorder` interface provides a minimal abstraction for recording metrics, improving testability and reducing coupling between components and specific metric implementations.

## Interface Definition

```go
type Recorder interface {
    RecordOperation(operation, status string)
    RecordDuration(operation string, seconds float64)
    RecordError(operation, errorType string)
}
```

## Benefits

1. **Improved Testability**: Use `TestRecorder` or `NoOpRecorder` in tests instead of real metrics
2. **Reduced Coupling**: Components depend on the interface, not concrete implementations
3. **Flexibility**: Easy to swap metric backends or add new implementations
4. **Simplicity**: Minimal interface covers most metric recording needs

## Available Implementations

### Production Implementations
- `BirdNETMetrics` - For BirdNET model operations
- `DatastoreMetrics` - For database operations
- Other concrete metric types implement this interface

### Test Implementations
- `TestRecorder` - Captures metrics for verification in tests
- `NoOpRecorder` - Does nothing, useful when metrics aren't needed

## Usage Examples

### 1. Component with Metrics

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

### 2. Testing with TestRecorder

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
}
```

### 3. Migration from Concrete Types

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

1. **Use descriptive operation names**: Be consistent with naming (e.g., "db_query", "cache_hit")
2. **Record both success and failure**: Always record operation status
3. **Measure critical paths**: Focus on operations that impact performance
4. **Keep it simple**: The interface is minimal by design - use it for common cases

## When to Use Concrete Types vs Interface

Use the **Recorder interface** when:
- The component needs basic metric recording (operations, durations, errors)
- You want to improve testability
- The component might be reused with different metric backends

Use **concrete metric types** when:
- You need access to specialized metrics (e.g., histograms, gauges)
- The component is tightly coupled to a specific metric implementation
- You need the full Prometheus collector interface