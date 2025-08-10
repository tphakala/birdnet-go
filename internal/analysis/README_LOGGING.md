# Analysis Package Logging Infrastructure

## Overview

This document describes the structured logging infrastructure implemented for the analysis package and its subpackages. Each package has its own dedicated logger with consistent patterns and defensive initialization.

## Logger Structure

### Package Loggers

| Package | Service Name | Logger File | Purpose |
|---------|--------------|-------------|---------|
| `analysis` | `analysis` | `logger.go` | Main analysis operations |
| `analysis.processor` | `species-tracking` | `new_species_tracker.go` | Processor operations (existing) |
| `analysis.jobqueue` | `analysis-jobqueue` | `logger.go` | Job queue operations |

### Logger Initialization Pattern

All loggers follow this pattern:

```go
var logger *slog.Logger

func init() {
    logger = logging.ForService("service-name")
    
    // Defensive initialization
    if logger == nil {
        logger = slog.Default().With("service", "service-name")
    }
}
```

## Usage Patterns

### Basic Logging

```go
logger.Info("Operation completed",
    "duration_ms", elapsed.Milliseconds(),
    "items_processed", count)
```

### Error Logging with Enhanced Errors

```go
if err := operation(); err != nil {
    enhancedErr := errors.New(err).
        Component("analysis").
        Category(errors.CategoryAudioAnalysis).
        Context("operation", "predict").
        Build()
    logger.Error("Operation failed", "error", enhancedErr)
}
```

### Performance Metrics

```go
start := time.Now()
// ... do work ...
logger.Debug("Processing completed",
    "duration_ms", time.Since(start).Milliseconds(),
    "chunks_processed", count)
```

## Helper Functions

### Analysis Package

- `GetLogger()` - Returns the package logger for use by other packages

### Processor Package

- `GetLogger()` - Returns the processor logger
- `LogDetectionProcessed()` - Logs detection processing
- `LogWorkerStarted()` - Logs worker start
- `LogWorkerCompleted()` - Logs worker completion

### JobQueue Package

- `GetLogger()` - Returns the jobqueue logger
- `LogJobEnqueued()` - Logs job enqueue
- `LogJobStarted()` - Logs job start
- `LogJobCompleted()` - Logs job completion
- `LogJobFailed()` - Logs job failure
- `LogQueueStats()` - Logs queue statistics

## Log Files

Based on the logging service configuration, logs will be written to:
- `logs/analysis.log` - Main analysis operations
- `logs/species-tracking.log` - Species tracking (existing)
- `logs/analysis-jobqueue.log` - Job queue operations

## Integration Notes

### Processor Package

The processor package already had a logger initialized in `new_species_tracker.go`. We've maintained compatibility by:
1. Keeping the existing logger setup
2. Adding helper functions in `logger.go`
3. Using the existing logger for all processor operations

### Error Integration

All loggers integrate with the enhanced error system:
- Errors logged will trigger telemetry if configured
- Error context is preserved in structured logs
- Component identification is automatic via error system

## Testing

When testing packages that use these loggers:

```go
func TestWithLogger(t *testing.T) {
    var buf bytes.Buffer
    testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    }))
    
    // Replace package logger for testing
    oldLogger := logger
    logger = testLogger
    defer func() { logger = oldLogger }()
    
    // Run test...
    
    // Verify logs
    logs := buf.String()
    assert.Contains(t, logs, "expected message")
}
```

## Migration Notes

### From fmt.Printf

```go
// Before
fmt.Printf("Processing chunk %d/%d\n", current, total)

// After
logger.Debug("Processing chunk",
    "current", current,
    "total", total)
```

### From log Package

```go
// Before
log.Printf("Error: %v", err)

// After
logger.Error("Operation failed", "error", err)
```

## Performance Considerations

1. **Log Levels**: Use appropriate levels (Debug for detailed info, Info for important events)
2. **Conditional Logging**: For expensive operations, check log level first:
   ```go
   if logger.Enabled(context.Background(), slog.LevelDebug) {
       logger.Debug("Expensive debug info", "data", expensiveOperation())
   }
   ```
3. **Structured Fields**: Use key-value pairs consistently for easy parsing

## Future Enhancements

- [ ] Log rotation configuration per package
- [ ] Dynamic log level adjustment
- [ ] Centralized log aggregation
- [ ] Metrics extraction from logs