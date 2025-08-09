# Internal/Logging Package Guidelines

## Critical Rules

### Service Logger Pattern (REQUIRED)

```go
package myservice

import (
    "github.com/tphakala/birdnet-go/internal/logging"
    "log/slog"
)

var logger *slog.Logger

func init() {
    logger = logging.ForService("myservice")
    // Defensive fallback for early startup
    if logger == nil {
        logger = slog.Default().With("service", "myservice")
    }
}
```

### Never Log Sensitive Data

```go
// ❌ FORBIDDEN
logger.Info("auth", "password", password, "token", token)

// ✅ CORRECT - Use privacy scrubbing
logger.Info("auth successful", "user_id", userID, "method", "oauth")
```

## Quick Patterns

### Standard Operations

```go
// Info with timing
start := time.Now()
result := processData()
logger.Info("data processed",
    "duration_ms", time.Since(start).Milliseconds(),
    "items", len(result))

// Error with context
logger.Error("operation failed",
    "error", err,
    "retry_count", retries,
    "operation", "save_detection")

// Debug (check if enabled for expensive ops)
if logger.Enabled(ctx, slog.LevelDebug) {
    logger.Debug("expensive debug info",
        "data", expensiveOperation())
}
```

### Key Naming Standards

```go
// REQUIRED key names for consistency:
"duration_ms"     // Time durations
"error"          // Error objects
"operation"      // Operation name
"component"      // Component name
"service"        // Service name (auto-added by ForService)
"status_code"    // HTTP status
"method"         // HTTP method
"path"           // URL path
"count"/"size"   // Quantities
"success"        // Boolean outcomes
```

## Service Loggers

### File Logger Creation

```go
// For specialized file logging
logger, closer, err := logging.NewFileLogger(
    "logs/myservice.log",
    "myservice",
    currentLogLevel)
defer closer()
```

### Standard Services (auto-created)

- birdweather, imageprovider, mqtt, notification
- weather, telemetry, events, init-manager

## Log Levels

```go
// Set global level
logging.SetLogLevel(logging.DebugLevel)

// Available levels
logging.ErrorLevel  // Errors only
logging.WarnLevel   // Warnings + errors
logging.InfoLevel   // Info + above (default)
logging.DebugLevel  // Everything
```

## Integration Requirements

### With Enhanced Errors

```go
err := errors.New(originalErr).
    Component("myservice").
    Category(errors.CategoryNetwork).
    Build()

logger.Error("operation failed",
    "error", err,  // Full enhanced error context
    "component", "myservice")
```

### Circular Dependency Avoidance

```go
// In init code, use fmt.Errorf instead of errors package
func initializeSystem() error {
    // ❌ AVOID in init code
    // return errors.New(err).Build()

    // ✅ CORRECT for init
    return fmt.Errorf("init failed: %w", err)
}
```

## Privacy & Performance

### Privacy Requirements (from #868)

- Always scrub sensitive data before logging
- Use structured logging for automatic scrubbing
- Never log: passwords, tokens, connection strings, full paths

### Performance Patterns

- Check log level before expensive operations
- Use async event bus for high-frequency errors
- Batch logging for bulk operations

## Troubleshooting

### Logger is nil?

1. Ensure `logging.Init()` called in main
2. Use defensive fallback pattern (shown above)
3. Check circular dependencies

### Missing logs?

1. Verify log level: `logging.SetLogLevel()`
2. Check file permissions for `logs/` directory
3. Ensure service logger initialized in `init()`

## Full Documentation

For details: `internal/logging/README.md`

- Architecture: lines 5-22
- Service patterns: lines 122-166
- Best practices: lines 168-209
- Privacy/security: lines 510-543
