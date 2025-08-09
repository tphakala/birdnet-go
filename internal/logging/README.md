# BirdNET-Go Logging Package

The logging package provides a comprehensive, structured logging system for BirdNET-Go applications with support for both human-readable console output and machine-parseable JSON file logs.

## Architecture

The logging system implements a dual-logger pattern:

1. **Console Logger**: Human-readable text format for stdout
2. **File Logger**: Structured JSON format for persistent storage and analysis

Both loggers share the same dynamic log level configuration, allowing runtime adjustments without restarts.

## Features

- **Structured Logging**: All logs use key-value pairs for consistent parsing
- **Service Isolation**: Each module maintains its own log file
- **Log Rotation**: Automatic rotation by size, daily, or weekly
- **Dynamic Levels**: Change log levels at runtime
- **Performance Metrics**: Built-in timing support
- **Error Integration**: Seamless integration with the enhanced error system

## Basic Usage

### 1. Main Application Logger

For the main application, use the global loggers:

```go
import "github.com/tphakala/birdnet-go/internal/logging"

// Console logging (human-readable)
logging.InfoConsole("Starting application", "version", "1.0.0")

// File logging (structured JSON)
logging.InfoFile("Application started", "version", "1.0.0", "pid", os.Getpid())
```

### 2. Service-Specific Logger

Most modules should create their own dedicated logger using `ForService()`:

```go
package myservice

import (
    "github.com/tphakala/birdnet-go/internal/logging"
    "log/slog"
)

var logger *slog.Logger

func init() {
    logger = logging.ForService("myservice")

    // Defensive initialization for early startup
    if logger == nil {
        logger = slog.Default().With("service", "myservice")
    }
}

func DoWork() {
    logger.Info("Processing started", "items", 42)

    // With timing
    start := time.Now()
    // ... do work ...
    logger.Debug("Processing completed",
        "duration_ms", time.Since(start).Milliseconds(),
        "processed", 42)
}
```

## Log Levels

Available log levels (from highest to lowest priority):

- `ErrorLevel` - Error conditions
- `WarnLevel` - Warning conditions
- `InfoLevel` - Informational messages
- `DebugLevel` - Debug messages

### Setting Log Levels

```go
// Set global log level
logging.SetLogLevel(logging.DebugLevel)

// Create logger with specific level
logger := logging.NewFileLogger("myservice", logging.WarnLevel)
```

## Configuration

Log files are configured through `conf.LogConfig`:

```go
type LogConfig struct {
    Enabled     bool         // Enable this log
    Path        string       // Path to log file
    Rotation    RotationType // Rotation type
    MaxSize     int64        // Max size for size-based rotation
    RotationDay string       // Day for weekly rotation
}
```

### Rotation Types

- `daily` - Rotate at midnight
- `weekly` - Rotate on specified day
- `size` - Rotate when file reaches MaxSize

### Example Configuration

```yaml
main:
  log:
    enabled: true
    path: "logs/app.log"
    rotation: "daily"
```

## Service Logger Pattern

The recommended pattern for service modules:

```go
package birdweather

import (
    "github.com/tphakala/birdnet-go/internal/logging"
    "log/slog"
)

var logger *slog.Logger

func init() {
    // Create service-specific logger
    logger = logging.ForService("birdweather")

    // Defensive initialization for early startup
    if logger == nil {
        logger = slog.Default().With("service", "birdweather")
    }
}

// Use throughout the module
func UploadDetection(detection Detection) error {
    logger.Debug("Starting upload",
        "species", detection.Species,
        "confidence", detection.Confidence)

    // ... perform upload ...

    if err != nil {
        logger.Error("Upload failed",
            "error", err,
            "species", detection.Species)
        return err
    }

    logger.Info("Upload successful",
        "species", detection.Species,
        "id", detection.ID)
    return nil
}
```

## Structured Logging Best Practices

### 1. Use Consistent Key Names

```go
// Good - consistent keys across the application
logger.Info("Request processed",
    "duration_ms", elapsed.Milliseconds(),
    "status_code", 200,
    "method", "GET",
    "path", "/api/detections")

// Bad - inconsistent naming
logger.Info("Request done",
    "time", elapsed,
    "code", 200,
    "req_method", "GET")
```

### 2. Include Context

```go
// Include relevant context for debugging
logger.Error("Database query failed",
    "query", "SELECT * FROM detections",
    "error", err,
    "retry_count", retries,
    "table", "detections")
```

### 3. Performance Metrics

```go
start := time.Now()
result, err := processData(data)
logger.Debug("Data processed",
    "duration_ms", time.Since(start).Milliseconds(),
    "input_size", len(data),
    "output_size", len(result),
    "success", err == nil)
```

## Integration with Error System

The logging package integrates seamlessly with the enhanced error system:

```go
import "github.com/tphakala/birdnet-go/internal/errors"

err := errors.New(originalErr).
    Component("birdweather").
    Category(errors.CategoryNetwork).
    Context("operation", "upload_detection").
    Build()

// Log with full error context
logger.Error("Operation failed",
    "error", err,
    "component", "birdweather")
```

### Event Bus Integration

The logging system integrates with the event bus for asynchronous error processing:

```go
// Errors are automatically published to the event bus
// Workers consume these events for telemetry and notifications
logger.Error("Critical error occurred",
    "error", enhancedErr,
    "severity", "critical")

// Performance benefit: 3,275x improvement over synchronous logging
// (100.78ms → 30.77μs)
```

## Common Patterns

### 1. Request/Response Logging

```go
func handleRequest(req *Request) {
    logger.Info("Request received",
        "method", req.Method,
        "path", req.Path,
        "client_ip", req.ClientIP)

    start := time.Now()
    resp, err := processRequest(req)

    logger.Info("Request completed",
        "method", req.Method,
        "path", req.Path,
        "status_code", resp.StatusCode,
        "duration_ms", time.Since(start).Milliseconds(),
        "error", err)
}
```

### 2. Batch Processing

```go
func processBatch(items []Item) {
    logger.Info("Batch processing started",
        "batch_size", len(items))

    processed := 0
    failed := 0

    for _, item := range items {
        if err := processItem(item); err != nil {
            failed++
            logger.Warn("Item processing failed",
                "item_id", item.ID,
                "error", err)
        } else {
            processed++
        }
    }

    logger.Info("Batch processing completed",
        "total", len(items),
        "processed", processed,
        "failed", failed)
}
```

### 3. Configuration Changes

```go
func updateConfiguration(newConfig Config) {
    oldLevel := currentLevel
    logger.Info("Configuration update",
        "old_level", oldLevel,
        "new_level", newConfig.LogLevel,
        "changed_by", "admin")

    applyConfig(newConfig)
}
```

### 4. State Transition Logging

```go
func initializeComponent() error {
    logger.Debug("initializing component")

    if err := setupDependencies(); err != nil {
        logger.Error("dependency setup failed", "error", err)
        return err
    }

    logger.Info("component initialized successfully",
        "dependencies", len(dependencies),
        "startup_time_ms", time.Since(start).Milliseconds())
    return nil
}
```

### 5. Performance Metrics Logging

```go
func reportPerformanceMetrics() {
    stats := getSystemStats()

    logger.Info("system performance metrics",
        "events_processed", stats.EventsProcessed,
        "events_dropped", stats.EventsDropped,
        "fast_path_percent", fmt.Sprintf("%.2f%%", stats.FastPathPercent),
        "memory_mb", stats.MemoryMB,
        "cpu_percent", stats.CPUPercent)
}
```

## Log File Locations

By default, log files are created in the `logs/` directory:

- `logs/app.log` - Main application log
- `logs/birdweather.log` - BirdWeather service
- `logs/imageprovider.log` - Image provider service
- `logs/mqtt.log` - MQTT service
- `logs/notification.log` - Notification service
- `logs/weather.log` - Weather service
- `logs/telemetry.log` - Telemetry service
- `logs/events.log` - Event bus operations
- `logs/init-manager.log` - Initialization coordination
- `logs/telemetry-integration.log` - Telemetry worker operations

## Testing

When testing modules that use logging:

```go
func TestMyFunction(t *testing.T) {
    // Create a test logger that writes to buffer
    var buf bytes.Buffer
    testLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    }))

    // Replace module logger for testing
    oldLogger := logger
    logger = testLogger
    defer func() { logger = oldLogger }()

    // Run test
    DoWork()

    // Verify logs
    logs := buf.String()
    assert.Contains(t, logs, "Processing started")
}
```

## Performance Considerations

1. **Log Level**: Use appropriate levels to control verbosity
2. **Conditional Logging**: Check level before expensive operations
   ```go
   if logger.Enabled(context.Background(), slog.LevelDebug) {
       logger.Debug("Expensive debug info",
           "data", expensiveOperation())
   }
   ```
3. **Batch Logging**: For high-frequency events, consider batching
4. **Async Logging**: File loggers write asynchronously by default

## Migration from Standard Log

To migrate from standard `log` package:

```go
// Before
log.Printf("Processing %d items", count)

// After
logger.Info("Processing items", "count", count)

// Before
log.Printf("Error: %v", err)

// After
logger.Error("Operation failed", "error", err)
```

## Troubleshooting

### Logger Returns Discard Writer

If `NewFileLogger` returns a discard logger, check:

1. Directory permissions for `logs/` folder
2. Disk space availability
3. File system errors in application logs

### Missing Logs

1. Verify log level settings
2. Check if logging is enabled in configuration
3. Ensure logger initialization in `init()`
4. Check file permissions and disk space

### Performance Impact

If logging impacts performance:

1. Reduce log level (e.g., Info instead of Debug)
2. Use conditional logging for expensive operations
3. Consider sampling for high-frequency events
4. Review log rotation settings

## Advanced Patterns

### Circular Dependency Avoidance

For initialization code that might create circular dependencies:

```go
package init_manager

import (
    "fmt" // Using fmt instead of errors package to avoid circular dependencies
    "github.com/tphakala/birdnet-go/internal/logging"
)

func initializeSystem() error {
    // Use standard errors in initialization to avoid:
    // telemetry → errors → telemetry circular dependency
    if err := validate(); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }

    logger := logging.ForService("init-manager")
    logger.Info("system initialization completed")
    return nil
}
```

### Defensive Logger Creation

```go
func getLoggerSafe(service string) *slog.Logger {
    logger := logging.ForService(service)
    if logger == nil {
        return slog.Default().With("service", service)
    }
    return logger
}
```

### Integration Health Monitoring

```go
func monitorIntegrationHealth() {
    logger := logging.ForService("health-monitor")

    // Check all integrated systems
    systems := []string{"telemetry", "events", "notifications"}

    for _, system := range systems {
        if health := checkSystemHealth(system); !health.OK {
            logger.Warn("system health check failed",
                "system", system,
                "error", health.Error,
                "last_success", health.LastSuccess)
        }
    }
}
```

## Best Practices Summary

1. **Always use `logging.ForService()`** for component identification
2. **Provide fallback loggers** for initialization scenarios
3. **Use defensive nil checks** before logging operations
4. **Avoid enhanced errors in initialization** code to prevent circular dependencies
5. **Log state transitions** for complex initialization sequences
6. **Include performance metrics** in periodic logging
7. **Use structured logging** with key-value pairs for operational data
8. **Implement graceful degradation** when logging systems aren't available
9. **Log integration health** for monitoring distributed components
10. **Use event bus integration** for async error processing when available

## Privacy and Security

### Privacy-Safe Logging

The telemetry system provides privacy scrubbing that integrates with logging:

```go
// Privacy scrubbing is automatically applied to telemetry logs
// Sensitive data is scrubbed before being sent to external services
logger.Info("user action logged",
    "action", "detection_upload",
    "user_id", userID, // Automatically scrubbed in telemetry
    "success", true)
```

### Secure Logging Practices

```go
// Never log sensitive data directly
logger.Info("authentication successful",
    "user_id", user.ID,
    "method", "oauth",
    // "password", password, // NEVER log passwords
    // "token", token,       // NEVER log tokens
)

// Use structured logging to avoid accidental sensitive data exposure
logger.Error("database connection failed",
    "error", err.Error(), // Error messages should not contain credentials
    "host", config.Host,
    "database", config.Database,
    // "connection_string", config.ConnectionString, // NEVER log connection strings
)
```

## Future Enhancements

Planned improvements:

- Structured query support for log analysis
- Integration with centralized logging systems
- Advanced filtering and routing
- Metrics extraction from logs
- Enhanced privacy scrubbing integration
- Distributed tracing correlation
