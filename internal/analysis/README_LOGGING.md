# Analysis Package Logging Infrastructure

## Overview

This document describes the structured logging infrastructure for the analysis package and its subpackages. All packages use the centralized `internal/logger` package with module-scoped loggers and `sync.Once` caching for efficiency.

## Logger Structure

### Package Loggers

| Package              | Module Path                        | Logger File              |
| -------------------- | ---------------------------------- | ------------------------ |
| `analysis`           | `analysis`                         | `logger.go`              |
| `analysis.processor` | `analysis.processor`               | `processor/logger.go`    |
| `analysis.soundlevel`| `analysis.soundlevel`              | `sound_level.go`         |
| `analysis.soundlevel.metrics` | `analysis.soundlevel.metrics` | `sound_level_metrics.go` |

### Logger Initialization Pattern

All package loggers use `sync.Once` to ensure efficient initialization:

```go
import (
    "sync"
    "github.com/tphakala/birdnet-go/internal/logger"
)

var (
    serviceLogger logger.Logger
    initOnce      sync.Once
)

// GetLogger returns the package logger scoped to the module.
// Uses sync.Once to ensure the logger is only initialized once.
func GetLogger() logger.Logger {
    initOnce.Do(func() {
        serviceLogger = logger.Global().Module("analysis")
    })
    return serviceLogger
}
```

For sub-modules, use hierarchical scoping:

```go
func getSoundLevelLogger() logger.Logger {
    soundLevelLoggerOnce.Do(func() {
        soundLevelLogger = logger.Global().Module("analysis").Module("soundlevel")
    })
    return soundLevelLogger
}
```

## Usage Patterns

### Basic Logging

Use type-safe field constructors from the logger package:

```go
log := GetLogger()
log.Info("operation completed",
    logger.Duration("duration", elapsed),
    logger.Int("items_processed", count))
```

### Error Logging

```go
if err := operation(); err != nil {
    GetLogger().Error("operation failed",
        logger.Error(err),
        logger.String("operation", "predict"))
}
```

### Debug Logging with Conditional Fields

```go
log := GetLogger()
log.Debug("processing chunk",
    logger.Int("current", current),
    logger.Int("total", total))
```

### Function-Level Caching

For functions with multiple log statements, cache the logger at function start:

```go
func processData(data []byte) error {
    log := GetLogger()  // Cache once at function start

    log.Debug("starting data processing",
        logger.Int("data_size", len(data)))

    // ... processing ...

    log.Info("data processing completed",
        logger.Duration("duration", elapsed))

    return nil
}
```

## Available Field Constructors

The `logger` package provides type-safe field constructors:

| Constructor | Usage |
|-------------|-------|
| `logger.String(key, value)` | String fields |
| `logger.Int(key, value)` | Integer fields |
| `logger.Int64(key, value)` | 64-bit integer fields |
| `logger.Float64(key, value)` | Float fields |
| `logger.Bool(key, value)` | Boolean fields |
| `logger.Duration(key, value)` | Duration fields |
| `logger.Time(key, value)` | Time fields |
| `logger.Error(err)` | Error fields (uses "error" key) |
| `logger.Any(key, value)` | Any type (use sparingly) |

## Helper Functions

The analysis package provides helper functions for common logging patterns:

```go
// In logger.go
LogSoundLevelMQTTPublished(topic, source string, bandCount int)
LogSoundLevelProcessorRegistered(source, sourceType, component string)
LogSoundLevelProcessorRegistrationFailed(source, sourceType, component string, err error)
LogSoundLevelProcessorUnregistered(source, sourceType, component string)
LogSoundLevelRegistrationSummary(successCount, totalCount, activeStreams int, partialSuccess bool, errors []error)
```

## Testing

When testing code that uses logging:

```go
import (
    "bytes"
    "io"
    "testing"
    "time"

    "github.com/tphakala/birdnet-go/internal/logger"
)

func TestWithLogging(t *testing.T) {
    // Option 1: Buffer logger to verify output
    buf := &bytes.Buffer{}
    testLogger := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)

    // Use testLogger in your component...

    // Verify logs
    logs := buf.String()
    assert.Contains(t, logs, "expected message")
}

func TestSilentLogging(t *testing.T) {
    // Option 2: Discard logger for silent tests
    testLogger := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)

    // Use testLogger in your component...
}
```

## Migration from Old Patterns

### From fmt.Printf

```go
// Before
fmt.Printf("Processing chunk %d/%d\n", current, total)

// After
GetLogger().Debug("processing chunk",
    logger.Int("current", current),
    logger.Int("total", total))
```

### From log Package

```go
// Before
log.Printf("Error: %v", err)

// After
GetLogger().Error("operation failed", logger.Error(err))
```

### From slog Direct Usage

```go
// Before
slog.Info("message", "key", value)

// After
GetLogger().Info("message", logger.String("key", value))
```

## Best Practices

1. **Use lowercase messages** - Log messages should be lowercase without trailing punctuation
2. **Cache loggers in functions** - For functions with multiple log calls, cache `GetLogger()` at the start
3. **Use type-safe constructors** - Always use `logger.String()`, `logger.Int()`, etc. instead of raw key-value pairs
4. **Module hierarchy** - Use `.Module()` chains for sub-components: `logger.Global().Module("analysis").Module("soundlevel")`
5. **Avoid emojis** - Keep log messages clean and professional
6. **Structured fields** - Use key-value pairs for all variable data, not string interpolation

## Performance Considerations

1. **sync.Once caching**: All `GetLogger()` functions use `sync.Once` for efficient repeated calls
2. **Log levels**: Use appropriate levels (Debug for verbose info, Info for important events, Warn/Error for issues)
3. **Avoid expensive operations in log calls**: Don't call expensive functions for log field values unless the log level is enabled
