# Logger Package

Centralized, module-aware logging system for BirdNET-Go built on Go's standard `log/slog`.

## Features

- **Dual Output Formats**:
  - **Console**: Human-readable text for development
  - **Files**: JSON for production log aggregation
- **Module-Scoped Logging**: Routes logs to module-specific files (`audio.log`, `analysis.log`, etc.)
- **Type-Safe Fields**: Structured logging with compile-time safety
- **Zero External Dependencies**: Built on Go standard library only
- **Echo Integration**: Adapter for Echo framework's internal logging
- **Finnish Locale**: Date/time formatting in DD.MM.YYYY HH:MM format

## Package Migration Quick Reference

When converting a package to use the centralized logger, follow this pattern:

### 1. Create Package Logger (logging.go)

```go
package mypackage

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the mypackage logger.
func GetLogger() logger.Logger {
    return logger.Global().Module("mypackage")
}

// Optional: Type alias for backwards compatibility
type MyPackageLogger = logger.Logger

// Optional: No-op for backwards compatibility
func CloseLogger() error {
    return nil
}
```

### 2. Usage Pattern in Functions

```go
func SomeFunction() error {
    log := GetLogger()  // ✅ Cache at function start

    log.Info("Starting operation", logger.String("key", "value"))

    // Use cached 'log' throughout the function
    for _, item := range items {
        log.Debug("Processing", logger.String("item", item))  // ✅ Reuses cached logger
    }

    return nil
}

// ❌ WRONG: Don't call GetLogger() inside loops
func BadFunction() {
    for _, item := range items {
        log := GetLogger()  // ❌ Creates new logger each iteration
        log.Info("Processing", logger.String("item", item))
    }
}
```

### 3. Closures Must Capture Logger

```go
func FunctionWithClosure() {
    log := GetLogger()  // Cache before closure

    handler := func(data string) {
        log.Info("Handled", logger.String("data", data))  // ✅ Uses captured logger
    }

    handler("test")
}
```

### 4. Test Files

```go
package mypackage

import (
    "github.com/tphakala/birdnet-go/internal/logger"
)

func cleanupTestArtifacts() {
    log := GetLogger()

    if err := os.RemoveAll("debug"); err != nil {
        log.Warn("Failed to remove debug directory", logger.Error(err))
    }
}
```

### 5. Checklist

- [ ] Remove `import "log"` and `import "log/slog"`
- [ ] Add `import "github.com/tphakala/birdnet-go/internal/logger"`
- [ ] Create `GetLogger()` function returning `logger.Global().Module("pkgname")`
- [ ] Replace `log.Printf(...)` with `log.Info(..., logger.String(...))` etc.
- [ ] Cache logger at function start: `log := GetLogger()`
- [ ] Never call `GetLogger()` inside loops
- [ ] Use structured fields: `logger.String()`, `logger.Int()`, `logger.Error()`, etc.
- [ ] Run `golangci-lint run ./internal/mypackage/...`
- [ ] Run `go test ./internal/mypackage/...`

---

## Quick Start

### Basic Usage

```go
import "github.com/tphakala/birdnet-go/internal/logger"

// Create central logger from config
cfg := &logger.LoggingConfig{
    DefaultLevel: "info",
    Timezone:     "Europe/Helsinki",
    Console: &logger.ConsoleOutput{
        Enabled: true,
        Level:   "info",
    },
    FileOutput: &logger.FileOutput{
        Enabled: true,
        Path:    "logs/app.log",
        Level:   "debug",
    },
}

centralLogger, err := logger.NewCentralLogger(cfg)
if err != nil {
    log.Fatal(err)
}
defer centralLogger.Close()

// Create module-scoped logger
appLogger := centralLogger.Module("main")

// Log with structured fields
appLogger.Info("Application started",
    logger.String("version", "1.0.0"),
    logger.Int("port", 8080))
```

### Output Examples

**Console (human-readable text)**:
```
[17.11.2025 09:59:51] INFO  [main] Application started version=1.0.0 port=8080
[17.11.2025 09:59:52] ERROR [api] Request failed status=500 error="timeout"
```

**File (JSON for machine parsing)**:
```json
{"time":"2025-11-17T09:59:51+02:00","level":"INFO","msg":"Application started","module":"main","version":"1.0.0","port":8080}
{"time":"2025-11-17T09:59:52+02:00","level":"ERROR","msg":"Request failed","module":"api","status":500,"error":"timeout"}
```

## Architecture

### Components

1. **CentralLogger**: Main logger factory, manages configuration and output routing
2. **Logger Interface**: Abstraction for dependency injection
3. **Text Handler**: Human-readable console output formatter
4. **Echo Adapter**: Bridges Echo framework to pkg/logger
5. **Module Routing**: Directs logs to appropriate files based on module name

### Output Strategy

```
┌─────────────────┐
│  Application    │
│     Code        │
└────────┬────────┘
         │
         v
┌─────────────────┐
│ CentralLogger   │
│  (pkg/logger)   │
└────┬───────┬────┘
     │       │
     v       v
  Console  Files
  (Text)   (JSON)
     │       │
     v       v
 Developer  Log
 Terminal   Aggregators
```

**Why Dual Format?**
- **Console**: Optimized for humans scanning logs during development
- **Files**: Optimized for machines parsing logs in production (ELK, Loki, etc.)

## Module-Scoped Logging

Route logs to dedicated files based on functionality:

```go
// Main application logger
mainLogger := centralLogger.Module("main")

// Audio processing operations -> audio.log
audioLogger := centralLogger.Module("audio")

// Bird detection analysis -> analysis.log
analysisLogger := centralLogger.Module("analysis")

// MQTT operations -> mqtt.log
mqttLogger := centralLogger.Module("mqtt")

// BirdWeather integration -> birdweather.log
birdweatherLogger := centralLogger.Module("birdweather")

// Nested modules (hierarchy)
realtimeLogger := analysisLogger.Module("realtime")
// Logs with module="analysis.realtime"
```

### Configuration for Module Files

```yaml
logging:
  default_level: "info"
  timezone: "Europe/Helsinki"
  console:
    enabled: true
    level: "info"
  modules:
    analysis:
      enabled: true
      file_path: "logs/analysis.log"
      level: "debug"
      console_also: false  # Don't duplicate to console
    mqtt:
      enabled: true
      file_path: "logs/mqtt.log"
      level: "info"
    birdweather:
      enabled: true
      file_path: "logs/birdweather.log"
      level: "info"
```

## Structured Logging

### Type-Safe Field Constructors

Always use typed field constructors for compile-time safety:

```go
// ✅ CORRECT: Type-safe fields
logger.Info("User login",
    logger.String("user_id", "123"),
    logger.String("ip", "192.168.1.1"),
    logger.Int("attempt", 1),
    logger.Bool("success", true),
    logger.Duration("elapsed", 150*time.Millisecond))

// ❌ WRONG: Old slog style (will not compile)
logger.Info("User login", "user_id", "123", "attempt", 1)
```

### Available Field Types

| Function | Type | Example |
|----------|------|---------|
| `logger.String(key, val)` | string | `logger.String("user", "alice")` |
| `logger.Int(key, val)` | int | `logger.Int("count", 42)` |
| `logger.Int64(key, val)` | int64 | `logger.Int64("bytes", 1024000)` |
| `logger.Bool(key, val)` | bool | `logger.Bool("enabled", true)` |
| `logger.Error(err)` | error | `logger.Error(err)` (key is "error") |
| `logger.Duration(key, val)` | time.Duration | `logger.Duration("timeout", 5*time.Second)` |
| `logger.Time(key, val)` | time.Time | `logger.Time("scheduled", t)` |
| `logger.Any(key, val)` | any | `logger.Any("data", complexStruct)` |

## Echo Framework Integration

Use the Echo adapter to route Echo's internal logs through pkg/logger:

```go
import (
    "github.com/tphakala/birdnet-go/internal/logger"
    "github.com/labstack/echo/v4"
)

// Create Echo server with logger adapter
e := echo.New()
e.HideBanner = true
e.HidePort = true

// Wire our logger adapter
echoLogger := appLogger.Module("echo")
e.Logger = logger.NewEchoLoggerAdapter(echoLogger)

// Echo's internal logs now use our logging system
e.Logger.Info("Starting server")  // Routes through pkg/logger
```

**Benefits**:
- All logs use consistent format
- Echo logs appear in unified log files
- Module scoping applies to Echo logs

## Logger Injection Patterns

### Constructor Injection (Preferred)

```go
type Handler struct {
    db     *sql.DB
    logger logger.Logger
}

func NewHandler(db *sql.DB, log logger.Logger) *Handler {
    // Defensive: provide fallback
    if log == nil {
        log = logger.NewSlogLogger(nil, logger.LogLevelInfo, nil)
    }

    return &Handler{
        db:     db,
        logger: log,
    }
}
```

### Post-Initialization Injection (Global Singletons)

For components initialized before logger (config manager, caches):

```go
// 1. Component has optional logger field
type ConfigManager struct {
    logger logger.Logger
}

// 2. Provide SetLogger() method
func (cm *ConfigManager) SetLogger(l logger.Logger) {
    cm.mu.Lock()
    defer cm.mu.Unlock()
    cm.logger = l
}

// 3. Log defensively (check for nil)
func (cm *ConfigManager) reload() {
    if cm.logger != nil {
        cm.logger.Info("Reloading config")
    }
}

// 4. In app initialization, inject logger
config.Instance().SetLogger(appLogger.Module("config"))
```

**Components using this pattern:**
- `config.Instance()` - Config file watcher
- `database.GetVendorCache()` - Vendor cache
- Echo framework - Via constructor parameter

## Context-Aware Logging

Extract trace IDs from context automatically:

```go
// Set trace ID in context
ctx := context.WithValue(ctx, "trace_id", "abc-123")

// Create context-aware logger
requestLogger := appLogger.WithContext(ctx)

// All logs include trace_id automatically
requestLogger.Info("Processing request")
// Output: {...,"trace_id":"abc-123","msg":"Processing request"}
```

## Field Accumulation

Build loggers with persistent fields:

```go
// Create request-scoped logger
requestLogger := appLogger.With(
    logger.String("request_id", "req-123"),
    logger.String("user_id", "user-456"),
)

// All subsequent logs include these fields
requestLogger.Info("Started processing")
requestLogger.Info("Completed processing")
// Both logs include request_id and user_id
```

## Testing

### Buffer Logger (Check Output)

```go
func TestService(t *testing.T) {
    buf := &bytes.Buffer{}
    testLogger := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)

    service := NewService(testLogger)
    service.DoWork()

    assert.Contains(t, buf.String(), "Work completed")
}
```

### Discard Logger (Silent Tests)

```go
func TestService(t *testing.T) {
    testLogger := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)

    service := NewService(testLogger)
    service.DoWork()  // No log output
}
```

## Log Levels

From most to least verbose:

1. **Trace**: Very detailed debugging (rarely used)
2. **Debug**: Detailed diagnostic information
3. **Info**: Normal operational messages (default)
4. **Warn**: Unexpected but recoverable events
5. **Error**: Errors requiring attention

```go
logger.Trace("Entering function", logger.String("func", "processData"))
logger.Debug("Processing item", logger.Int("item_id", 42))
logger.Info("Request completed", logger.Duration("elapsed", dur))
logger.Warn("Retrying operation", logger.Int("attempt", 2))
logger.Error("Operation failed", logger.Error(err))
```

## Configuration

### YAML Example

```yaml
logging:
  default_level: "info"
  timezone: "Europe/Helsinki"

  console:
    enabled: true
    level: "info"
    pretty: false  # Future: color support

  file_output:
    enabled: true
    path: "logs/app.log"
    level: "debug"
    max_size: 100      # MB
    max_age: 30        # days
    max_backups: 10
    compress: true

  module_levels:
    datastore: "debug"
    api: "info"
    analysis: "debug"

  modules:
    analysis:
      enabled: true
      file_path: "logs/analysis.log"
      level: "debug"
      console_also: false
    mqtt:
      enabled: true
      file_path: "logs/mqtt.log"
      level: "info"
    birdweather:
      enabled: true
      file_path: "logs/birdweather.log"
      level: "info"
```

### JSON Example

```json
{
  "logging": {
    "default_level": "info",
    "timezone": "Europe/Helsinki",
    "console": {
      "enabled": true,
      "level": "info"
    },
    "file_output": {
      "enabled": true,
      "path": "logs/app.log",
      "level": "debug"
    }
  }
}
```

## Best Practices

### DO

✅ Use module scoping to identify log sources:
```go
logger := centralLogger.Module("api")
```

✅ Use structured fields, not string concatenation:
```go
logger.Info("Request received", logger.String("path", path))
```

✅ Log errors with context:
```go
logger.Error("Failed to save user",
    logger.Error(err),
    logger.String("user_id", id),
    logger.String("operation", "save"))
```

✅ Call `Flush()` and `Close()` on shutdown:
```go
defer centralLogger.Close()
```

### DON'T

❌ Don't use string formatting in log messages:
```go
// Bad
logger.Info(fmt.Sprintf("User %s logged in", userID))

// Good
logger.Info("User logged in", logger.String("user_id", userID))
```

❌ Don't log sensitive data:
```go
// Bad - logs password!
logger.Info("Login attempt", logger.String("password", pwd))

// Good
logger.Info("Login attempt", logger.String("user", username))
```

❌ Don't mix log levels inappropriately:
```go
// Bad - normal operation is not an error
logger.Error("Request completed successfully")

// Good
logger.Info("Request completed successfully")
```

## Performance Considerations

- **Log level checks happen before field evaluation** - Disabled log levels have minimal overhead
- **Structured fields avoid string concatenation** - More efficient than fmt.Sprintf
- **File I/O is buffered** - Call `Flush()` to ensure writes
- **Module routing is cached** - No performance penalty for nested modules

## Migration Guide

### From Standard log Package

```go
// Old
import "log"
log.Printf("User logged in: %s", userID)

// New
import "github.com/tphakala/birdnet-go/internal/logger"
logger.Info("User logged in", logger.String("user_id", userID))
```

### From Direct slog

```go
// Old
import "log/slog"
slog.Error("Failed", "key", value, "key2", value2)

// New
import "github.com/tphakala/birdnet-go/internal/logger"
logger.Error("Failed",
    logger.String("key", value),
    logger.Int("key2", value2))
```

### From fmt.Println

```go
// Old
fmt.Println("Loading cache...")
fmt.Printf("Loaded %d items\n", count)

// New
logger.Info("Loading cache")
logger.Info("Cache loaded", logger.Int("item_count", count))
```

## Troubleshooting

### Logs not appearing

**Check log level configuration**:
```go
// Console level too restrictive?
Console: &logger.ConsoleOutput{
    Enabled: true,
    Level:   "error",  // Only shows errors
}
```

**Check module configuration**:
```yaml
modules:
  mymodule:
    enabled: true  # Make sure this is true
    level: "debug"
```

### JSON appearing in console

**Verify text handler is configured** (should be automatic):
```go
// CentralLogger automatically uses text handler for console
// If you're creating SlogLogger directly:
logger := logger.NewSlogLogger(os.Stdout, ...)  // Uses JSON
// Use CentralLogger instead for text console output
```

### Echo logs not appearing

**Check Echo adapter is wired**:
```go
e.Logger = logger.NewEchoLoggerAdapter(appLogger.Module("echo"))
```

## See Also

- [Go slog documentation](https://pkg.go.dev/log/slog)
- [internal/logger godoc](https://pkg.go.dev/github.com/tphakala/birdnet-go/internal/logger)
