# Logging System - Quick Reference

## Setup (3 steps)

```go
// 1. Initialize in main.go
centralLogger, _ := logger.NewCentralLogger(&cfg.Logging)
defer centralLogger.Close()

// 2. Create module loggers
appLogger := centralLogger.Module("birdnet")
datastoreLogger := centralLogger.Module("datastore")

// 3. Inject into components
storage := NewStorage(datastoreLogger)
```

## Basic Usage

```go
// Info logging
logger.Info("User logged in",
    logger.String("user_id", "123"),
    logger.String("ip", "192.168.1.1"))

// Error logging
logger.Error("Operation failed",
    logger.Error(err),
    logger.String("operation", "save"))

// Debug logging (only if level allows)
logger.Debug("Processing step",
    logger.String("step", "validation"))
```

## Field Types

```go
logger.String("key", "value")           // String field
logger.Int("count", 42)                 // Integer
logger.Int64("bignum", int64(123))      // 64-bit integer
logger.Bool("enabled", true)            // Boolean
logger.Error(err)                       // Error (key is always "error")
logger.Duration("elapsed", 5*time.Second) // Duration
logger.Time("timestamp", time.Now())    // Time
logger.Any("data", complexStruct)       // Any value (JSON)
```

## Context-Aware Logging

```go
// Add trace_id to context
ctx := context.WithValue(ctx, "trace_id", "abc-123")

// Logger extracts trace_id automatically
contextLogger := logger.WithContext(ctx)
contextLogger.Info("Processing")  // Includes trace_id
```

## Field Accumulation

```go
// Create logger with persistent fields
requestLogger := logger.With(
    logger.String("request_id", "req-123"),
    logger.String("user_id", "user-456"))

// All subsequent logs include these fields
requestLogger.Info("Started")
requestLogger.Info("Completed")
```

## Module Scoping

```go
// Create nested modules
datastoreLogger := centralLogger.Module("datastore")
sqliteLogger := datastoreLogger.Module("sqlite")

sqliteLogger.Info("Query executed")
// Output: module="datastore.sqlite"
```

## Configuration (config.yaml)

```yaml
logging:
  default_level: "info"  # trace, debug, info, warn, error
  timezone: "Local"      # "Local", "UTC", or IANA name like "Europe/Helsinki"

  # Console: text format, no timestamps (journald/Docker adds them)
  console:
    enabled: true
    level: "info"

  # File: JSON format with RFC3339 timestamps
  file_output:
    enabled: true
    path: "logs/app.log"
    level: "debug"

  module_levels:
    storage: "debug"  # Per-module level override
    auth: "info"

  modules:
    auth:
      enabled: true
      file_path: "logs/auth.log"  # Dedicated file
      level: "info"
      console_also: false
```

## Log Levels

```go
logger.Trace("Very verbose debugging")     // -8 (rarely used)
logger.Debug("Detailed diagnostics")       // -4
logger.Info("Normal operations")           // 0 (default)
logger.Warn("Unexpected but recoverable")  // 4
logger.Error("Errors requiring attention") // 8
```

## Component Template

```go
package mypackage

import "yourproject/pkg/logger"

type MyComponent struct {
    logger logger.Logger  // Always inject interface
}

func NewMyComponent(log logger.Logger) (*MyComponent, error) {
    if log == nil {
        return nil, errors.New("logger is required")
    }

    log.Info("Initializing component")

    return &MyComponent{logger: log}, nil
}

func (c *MyComponent) Process(ctx context.Context, data *Data) error {
    start := time.Now()

    c.logger.Debug("Processing started",
        logger.String("data_id", data.ID))

    if err := c.doWork(ctx, data); err != nil {
        c.logger.Error("Processing failed",
            logger.Error(err),
            logger.String("data_id", data.ID))
        return err
    }

    c.logger.Info("Processing completed",
        logger.String("data_id", data.ID),
        logger.Duration("duration", time.Since(start)))

    return nil
}
```

## Testing

```go
// Option 1: Buffer logger (check output)
buf := &bytes.Buffer{}
testLogger := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)
handler := NewHandler(testLogger)
// Assert buf.String() contains expected logs

// Option 2: Discard logger (silent)
testLogger := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
handler := NewHandler(testLogger)
// No log output during tests

// Option 3: Mock logger (advanced)
mockLogger := mocks.NewMockLogger(t)
mockLogger.EXPECT().Info("Processing", mock.Anything).Once()
handler := NewHandler(mockLogger)
```

## Common Patterns

### HTTP Handler Logging
```go
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    start := time.Now()
    logger := h.logger.WithContext(r.Context())

    logger.Info("Request received",
        logger.String("method", r.Method),
        logger.String("path", r.URL.Path))

    // ... handle request ...

    logger.Info("Request completed",
        logger.Int("status", status),
        logger.Duration("duration", time.Since(start)))
}
```

### Error Handling
```go
if err := operation(); err != nil {
    logger.Error("Operation failed",
        logger.Error(err),
        logger.String("operation", "save_user"),
        logger.String("user_id", userID),
        logger.Int("attempt", retryCount))
    return fmt.Errorf("failed to save user: %w", err)
}
```

### Event Logging
```go
const (
    EventUserLogin = "user_login"
    EventOrderCreated = "order_created"
)

logger.Info("User event",
    logger.String("event_type", EventUserLogin),
    logger.String("user_id", userID),
    logger.String("ip", clientIP))
```

### Performance Metrics
```go
func (s *Service) Process() error {
    start := time.Now()
    defer func() {
        s.logger.Info("Processing metrics",
            logger.Duration("total_duration", time.Since(start)))
    }()

    parseStart := time.Now()
    data, _ := s.parse()
    parseDuration := time.Since(parseStart)

    s.logger.Debug("Parse complete",
        logger.Duration("parse_duration", parseDuration),
        logger.Int("items", len(data)))

    // ... continue processing
}
```

## Best Practices

✅ **DO**
- Inject `logger.Logger` interface, not concrete types
- Use structured fields, not string concatenation
- Create module-scoped loggers for each package
- Log errors with context (operation, IDs, attempt count)
- Include timing metrics for operations
- Use constants for event types
- Flush and close logger on shutdown

❌ **DON'T**
- Don't use `fmt.Sprintf()` for log messages
- Don't log sensitive data (passwords, tokens, PII)
- Don't use string concatenation in log messages
- Don't create global loggers (inject via DI)
- Don't forget nil checks on injected loggers
- Don't log at trace/debug level in hot paths

## Troubleshooting

**Logs not appearing?**
- Check log level (might be filtering out messages)
- Verify console/file enabled in config
- Call `logger.Flush()` before exiting
- Check file permissions for log directory

**Performance issues?**
- Reduce log level in production
- Avoid logging in tight loops
- Check file I/O isn't blocking
- Consider async logging for high throughput

**Missing trace IDs?**
- Ensure context has "trace_id" value
- Use `logger.WithContext(ctx)` to extract
- Check middleware sets trace_id in context

## Output Examples

**JSON (Production)**
```json
{
  "time": "2025-01-12T10:30:00Z",
  "level": "INFO",
  "msg": "User logged in",
  "module": "auth",
  "user_id": "user-123",
  "ip": "192.168.1.1",
  "trace_id": "abc-123"
}
```

**Pretty (Development)**
```
2025-01-12 10:30:00 INFO  [auth] User logged in user_id=user-123 ip=192.168.1.1 trace_id=abc-123
```

## File Structure

```
yourproject/
├── pkg/
│   └── logger/
│       ├── logger.go          # Interface + field constructors
│       ├── slog_logger.go     # slog implementation
│       ├── central_logger.go  # Module routing
│       ├── config.go          # Configuration structs
│       └── multiwriter.go     # Multi-handler support
├── internal/
│   ├── auth/
│   │   └── handler.go         # Uses logger.Logger
│   ├── storage/
│   │   └── storage.go         # Uses logger.Logger
│   └── ...
├── cmd/
│   └── myapp/
│       └── main.go            # Initializes CentralLogger
├── config.yaml                # Logging configuration
└── logs/                      # Log output directory
    ├── app.log
    ├── auth.log
    └── storage.log
```

## Quick Start Checklist

- [ ] Copy logger package to `pkg/logger/`
- [ ] Update import paths
- [ ] Add `LoggingConfig` to config structure
- [ ] Add logging section to `config.yaml`
- [ ] Initialize `CentralLogger` in `main.go`
- [ ] Create module-scoped loggers
- [ ] Update component constructors to accept `Logger` interface
- [ ] Replace old log calls with structured logging
- [ ] Add `defer centralLogger.Close()` in main
- [ ] Test with different log levels
- [ ] Setup log rotation (logrotate or SIGHUP)

---

For detailed documentation, see [LOGGING_IMPLEMENTATION_GUIDE.md](LOGGING_IMPLEMENTATION_GUIDE.md)
