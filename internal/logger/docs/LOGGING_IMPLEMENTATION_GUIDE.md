# Structured Logging System Implementation Guide

This guide explains how to implement the HookRelay structured logging system in your own Go projects. The system is built on Go's standard `log/slog` library with additional features for module-aware, dependency-injectable logging.

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [Core Components](#core-components)
3. [Quick Start](#quick-start)
4. [Implementation Steps](#implementation-steps)
5. [Configuration](#configuration)
6. [Usage Patterns](#usage-patterns)
7. [Advanced Features](#advanced-features)
8. [Testing with Mocks](#testing-with-mocks)
9. [Best Practices](#best-practices)

---

## Architecture Overview

### Design Principles

The logging system follows these key principles:

1. **Interface-Based Design**: All components depend on the `Logger` interface, not concrete implementations
2. **Dependency Injection**: Loggers are injected into components via constructors
3. **Module-Aware**: Each component can have its own scoped logger (e.g., `webhook`, `storage`, `auth`)
4. **Structured Logging**: All logs use structured fields (key-value pairs), not string concatenation
5. **Flexible Routing**: Logs can be routed to different outputs based on module (console, file, per-module files)
6. **Built on `log/slog`**: Uses Go's standard library for maximum compatibility

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                      Application                            │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                 │
│  │ Handler  │  │ Storage  │  │ Executor │  ...             │
│  │ logger   │  │ logger   │  │ logger   │                  │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘                 │
│       │             │             │                         │
│       └─────────────┴─────────────┘                         │
│                     │                                       │
│            ┌────────▼────────┐                             │
│            │  Logger Interface│  (Injected)                 │
│            └────────┬────────┘                             │
│                     │                                       │
│       ┌─────────────┼─────────────┐                        │
│       │             │             │                         │
│  ┌────▼────┐  ┌────▼────┐  ┌────▼────┐                   │
│  │ Module  │  │ Module  │  │ Module  │                    │
│  │ Logger  │  │ Logger  │  │ Logger  │                    │
│  │ (main)  │  │(storage)│  │ (auth)  │                    │
│  └────┬────┘  └────┬────┘  └────┬────┘                   │
│       │            │            │                          │
│       └────────────┴────────────┘                          │
│                    │                                       │
│         ┌──────────▼──────────┐                           │
│         │  CentralLogger      │                            │
│         │  - Base Handler     │                            │
│         │  - Module Routing   │                            │
│         │  - File Management  │                            │
│         └──────────┬──────────┘                           │
│                    │                                       │
│         ┌──────────┴──────────┐                           │
│         │                     │                           │
│    ┌────▼─────┐        ┌─────▼──────┐                    │
│    │ Console  │        │ File(s)    │                     │
│    │ (stdout) │        │ (JSON logs)│                     │
│    └──────────┘        └────────────┘                     │
└─────────────────────────────────────────────────────────────┘
```

---

## Core Components

### 1. Logger Interface (`pkg/logger/logger.go`)

The central interface that all components depend on:

```go
type Logger interface {
    // Module returns a logger scoped to a specific module
    Module(name string) Logger

    // Leveled logging methods
    Trace(msg string, fields ...Field)
    Debug(msg string, fields ...Field)
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)

    // Context-aware logging
    With(fields ...Field) Logger
    WithContext(ctx context.Context) Logger

    // Log with explicit level
    Log(level LogLevel, msg string, fields ...Field)

    // Flush ensures all buffered logs are written
    Flush() error
}
```

**Key features**:
- Module scoping for hierarchical loggers (e.g., `main.datastore.sqlite`)
- Structured fields via type-safe constructors
- Context propagation for trace IDs and request correlation
- Explicit log levels: `trace`, `debug`, `info`, `warn`, `error`

### 2. Field Types (`pkg/logger/logger.go`)

Type-safe field constructors for structured logging:

```go
type Field struct {
    Key   string
    Value any
}

// Field constructors
func String(key, value string) Field
func Int(key string, value int) Field
func Int64(key string, value int64) Field
func Bool(key string, value bool) Field
func Error(err error) Field
func Duration(key string, value time.Duration) Field
func Time(key string, value time.Time) Field
func Any(key string, value any) Field
```

### 3. CentralLogger (`pkg/logger/central_logger.go`)

Manages module-aware logging with flexible routing:

```go
type CentralLogger struct {
    config       *LoggingConfig
    timezone     *time.Location
    baseHandler  slog.Handler          // Default handler
    moduleFiles  map[string]*os.File   // Per-module file handles
    moduleLevels map[string]slog.Level // Per-module log levels
    mu           sync.RWMutex
}

func NewCentralLogger(cfg *LoggingConfig) (*CentralLogger, error)
func (cl *CentralLogger) Module(name string) Logger
func (cl *CentralLogger) Close() error
func (cl *CentralLogger) Flush() error
```

**Features**:
- Routes logs to different outputs based on module
- Supports console output (JSON or pretty-print)
- Supports main file output and per-module file outputs
- Thread-safe with mutex protection
- Configurable log levels per module

### 4. SlogLogger (`pkg/logger/slog_logger.go`)

The concrete implementation built on Go's `log/slog`:

```go
type SlogLogger struct {
    handler  slog.Handler
    level    slog.Level
    module   string
    timezone *time.Location
    fields   []Field
    logFile  *os.File
    filePath string
    mu       sync.RWMutex
}

func NewSlogLogger(writer io.Writer, level LogLevel, timezone *time.Location) *SlogLogger
func NewSlogLoggerWithFile(filePath string, level LogLevel, timezone *time.Location) (*SlogLogger, error)
func (l *SlogLogger) ReopenLogFile() error  // For log rotation (SIGHUP)
```

**Features**:
- JSON output format by default
- File output with log rotation support (via SIGHUP)
- Module scoping with nested names
- Field accumulation with `With()`
- Context-aware logging with trace ID extraction
- Thread-safe file operations

### 5. Configuration (`pkg/logger/config.go`)

YAML-based configuration structure:

```go
type LoggingConfig struct {
    DefaultLevel  string                  `yaml:"default_level"`   // "debug", "info", "warn", "error"
    Timezone      string                  `yaml:"timezone"`        // e.g., "Europe/Helsinki"
    Console       *ConsoleOutput          `yaml:"console"`
    FileOutput    *FileOutput             `yaml:"file_output"`
    ModuleOutputs map[string]ModuleOutput `yaml:"modules"`         // Per-module outputs
    ModuleLevels  map[string]string       `yaml:"module_levels"`   // Per-module levels
    DebugWebhooks bool                    `yaml:"debug_webhooks"`  // App-specific flag
}

type ConsoleOutput struct {
    Enabled bool   `yaml:"enabled"`
    Pretty  bool   `yaml:"pretty"`  // Human-readable vs JSON
    Level   string `yaml:"level"`
}

type FileOutput struct {
    Enabled    bool   `yaml:"enabled"`
    Path       string `yaml:"path"`
    MaxSize    int    `yaml:"max_size"`    // MB (not implemented yet)
    MaxAge     int    `yaml:"max_age"`     // days (not implemented yet)
    MaxBackups int    `yaml:"max_backups"` // (not implemented yet)
    Compress   bool   `yaml:"compress"`    // (not implemented yet)
    Level      string `yaml:"level"`
}

type ModuleOutput struct {
    Enabled     bool   `yaml:"enabled"`
    FilePath    string `yaml:"file_path"`    // Dedicated file for this module
    Level       string `yaml:"level"`        // Override level for module
    ConsoleAlso bool   `yaml:"console_also"` // Also log to console
}
```

---

## Quick Start

### Minimal Example (5 minutes)

```go
package main

import (
    "context"
    "time"
    "github.com/yourorg/yourproject/pkg/logger"
)

func main() {
    // 1. Create configuration
    cfg := &logger.LoggingConfig{
        DefaultLevel: "info",
        Timezone:     "UTC",
        Console: &logger.ConsoleOutput{
            Enabled: true,
            Level:   "info",
        },
    }

    // 2. Create central logger
    centralLogger, err := logger.NewCentralLogger(cfg)
    if err != nil {
        panic(err)
    }
    defer centralLogger.Close()

    // 3. Create module-scoped loggers
    appLogger := centralLogger.Module("birdnet")
    datastoreLogger := centralLogger.Module("datastore")

    // 4. Use structured logging
    appLogger.Info("Application starting",
        logger.String("version", "1.0.0"),
        logger.Int("workers", 10))

    datastoreLogger.Debug("Connecting to database",
        logger.String("host", "localhost"),
        logger.Int("port", 5432))

    // 5. Context-aware logging
    ctx := context.WithValue(context.Background(), "trace_id", "abc-123")
    contextLogger := appLogger.WithContext(ctx)
    contextLogger.Info("Processing request")  // Automatically includes trace_id

    // 6. Error logging
    err = doSomething()
    if err != nil {
        appLogger.Error("Operation failed",
            logger.Error(err),
            logger.String("operation", "doSomething"))
    }
}
```

---

## Implementation Steps

### Step 1: Copy Core Files

Copy these files to your project:

```bash
mkdir -p pkg/logger
cp logger.go pkg/logger/           # Interface + field constructors
cp slog_logger.go pkg/logger/      # slog implementation
cp central_logger.go pkg/logger/   # Central routing
cp config.go pkg/logger/           # Configuration structs
cp multiwriter.go pkg/logger/      # Multi-handler support
```

### Step 2: Update Import Paths

Change all import paths from:
```go
import "github.com/tphakala/hookrelay/pkg/logger"
```

To your project's import path:
```go
import "github.com/yourorg/yourproject/pkg/logger"
```

### Step 3: Add Configuration to Your Config File

Add logging configuration to your `config.yaml`:

```yaml
logging:
  default_level: "info"
  timezone: "UTC"  # or "America/New_York", "Europe/Helsinki", etc.

  console:
    enabled: true
    level: "info"
    pretty: false  # true for dev, false for production

  file_output:
    enabled: true
    path: "logs/app.log"
    level: "debug"

  # Optional: Per-module log levels
  module_levels:
    storage: "debug"
    auth: "info"
    api: "warn"

  # Optional: Per-module file outputs
  modules:
    storage:
      enabled: true
      file_path: "logs/storage.log"
      level: "debug"
      console_also: false

    auth:
      enabled: true
      file_path: "logs/auth.log"
      level: "info"
      console_also: true  # Also log to console
```

### Step 4: Initialize in main.go

```go
package main

import (
    "log"
    "github.com/yourorg/yourproject/internal/config"
    "github.com/yourorg/yourproject/pkg/logger"
)

func main() {
    // Load config
    cfg, err := config.LoadFromFile("config.yaml")
    if err != nil {
        log.Fatalf("Failed to load config: %v", err)
    }

    // Create central logger
    centralLogger, err := logger.NewCentralLogger(&cfg.Logging)
    if err != nil {
        log.Fatalf("Failed to create logger: %v", err)
    }
    defer func() {
        if err := centralLogger.Flush(); err != nil {
            log.Printf("Failed to flush logger: %v", err)
        }
        if err := centralLogger.Close(); err != nil {
            log.Printf("Failed to close logger: %v", err)
        }
    }()

    // Create module-scoped loggers
    appLogger := centralLogger.Module("birdnet")

    appLogger.Info("Application starting",
        logger.String("version", version))

    // Pass module loggers to components
    storage, err := NewStorage(cfg, centralLogger.Module("datastore"))
    authHandler := NewAuthHandler(cfg, centralLogger.Module("auth"))
    apiServer := NewAPIServer(cfg, centralLogger.Module("api"))

    // ... rest of your application
}
```

### Step 5: Use in Components (Dependency Injection)

```go
package storage

import (
    "context"
    "github.com/yourorg/yourproject/pkg/logger"
)

// Storage depends on Logger interface
type Storage struct {
    db     *sql.DB
    logger logger.Logger  // Injected interface
}

// NewStorage accepts injected logger
func NewStorage(cfg *Config, log logger.Logger) (*Storage, error) {
    if log == nil {
        return nil, errors.New("logger is required")
    }

    log.Info("Initializing storage",
        logger.String("backend", cfg.Backend))

    db, err := sql.Open(cfg.Driver, cfg.DSN)
    if err != nil {
        log.Error("Failed to open database",
            logger.Error(err),
            logger.String("driver", cfg.Driver))
        return nil, err
    }

    return &Storage{
        db:     db,
        logger: log,
    }, nil
}

// Use logger throughout the component
func (s *Storage) GetUser(ctx context.Context, id string) (*User, error) {
    start := time.Now()

    s.logger.Debug("Fetching user",
        logger.String("user_id", id))

    user, err := s.queryUser(ctx, id)
    if err != nil {
        s.logger.Error("Failed to fetch user",
            logger.String("user_id", id),
            logger.Error(err))
        return nil, err
    }

    s.logger.Info("User fetched",
        logger.String("user_id", id),
        logger.Duration("duration", time.Since(start)))

    return user, nil
}
```

---

## Configuration

### Configuration Options

#### Global Settings

```yaml
logging:
  default_level: "info"  # Default for all modules: trace, debug, info, warn, error
  timezone: "UTC"        # Timezone for timestamps
```

#### Console Output

```yaml
  console:
    enabled: true
    level: "info"   # Can be different from default_level
    pretty: false   # true: colored, human-readable; false: JSON
```

**When to use**:
- `pretty: true` for local development (readable output)
- `pretty: false` for production (machine-parseable JSON)

#### File Output

```yaml
  file_output:
    enabled: true
    path: "logs/app.log"
    level: "debug"  # Log more verbose to file
```

**Best practices**:
- Use absolute paths or relative to working directory
- Create `logs/` directory in `.gitignore`
- Use lower level in file (debug) than console (info)

#### Module-Specific Levels

```yaml
  module_levels:
    storage: "debug"    # Storage module logs at debug level
    auth: "info"        # Auth module logs at info level
    webhook: "trace"    # Webhook module logs everything
```

**Use cases**:
- Debug specific components without flooding logs
- Reduce noise from chatty modules
- Compliance requirements (log all auth events)

#### Module-Specific Outputs

```yaml
  modules:
    auth:
      enabled: true
      file_path: "logs/auth.log"
      level: "info"
      console_also: true  # Also output to console
```

**Use cases**:
- Security logs in separate file for auditing
- High-volume modules (metrics, health checks)
- Compliance requirements (separate auth/payment logs)

### Example Configurations

#### Development Configuration

```yaml
logging:
  default_level: "debug"
  timezone: "America/New_York"

  console:
    enabled: true
    level: "debug"
    pretty: true  # Human-readable output

  file_output:
    enabled: false  # No file logging in dev
```

#### Production Configuration

```yaml
logging:
  default_level: "info"
  timezone: "UTC"

  console:
    enabled: true
    level: "info"
    pretty: false  # JSON for log aggregation

  file_output:
    enabled: true
    path: "/var/log/myapp/app.log"
    level: "debug"  # More verbose in file

  module_levels:
    auth: "info"    # Always log auth events
    health: "warn"  # Reduce health check noise

  modules:
    auth:
      enabled: true
      file_path: "/var/log/myapp/auth.log"
      level: "info"
      console_also: false  # Only to file
```

#### Testing Configuration

```yaml
logging:
  default_level: "error"  # Only errors in tests
  timezone: "UTC"

  console:
    enabled: false  # Silent during tests

  file_output:
    enabled: false
```

---

## Usage Patterns

### 1. Basic Structured Logging

```go
logger.Info("User logged in",
    logger.String("user_id", userID),
    logger.String("ip", clientIP),
    logger.Duration("session_duration", duration))
```

**Output (JSON)**:
```json
{
  "time": "2025-01-12T10:30:00Z",
  "level": "INFO",
  "msg": "User logged in",
  "user_id": "user-123",
  "ip": "192.168.1.1",
  "session_duration": "1h30m"
}
```

### 2. Module Scoping

```go
// In main.go
appLogger := centralLogger.Module("birdnet")
datastoreLogger := centralLogger.Module("datastore")
authLogger := centralLogger.Module("auth")

// Pass to components
storage := NewStorage(datastoreLogger)
auth := NewAuthHandler(authLogger)

// In storage component
s.logger.Info("Connected to database")  // Output: module="datastore"

// Nested modules
sqliteLogger := datastoreLogger.Module("sqlite")
sqliteLogger.Debug("Executing query")  // Output: module="datastore.sqlite"
```

### 3. Context-Aware Logging (Trace IDs)

```go
// In HTTP middleware - add trace ID to context
func traceMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        traceID := uuid.New().String()
        ctx := context.WithValue(r.Context(), "trace_id", traceID)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

// In handler - logger extracts trace_id from context
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    logger := h.logger.WithContext(r.Context())

    logger.Info("Processing request")  // Automatically includes trace_id

    if err := h.process(r); err != nil {
        logger.Error("Request failed", logger.Error(err))
        // Both logs will have the same trace_id
    }
}
```

**Output**:
```json
{"time":"...","level":"INFO","msg":"Processing request","trace_id":"abc-123"}
{"time":"...","level":"ERROR","msg":"Request failed","trace_id":"abc-123","error":"..."}
```

### 4. Field Accumulation with With()

```go
// Create logger with persistent fields
requestLogger := h.logger.With(
    logger.String("request_id", requestID),
    logger.String("user_id", userID),
    logger.String("ip", clientIP))

// All subsequent logs include these fields
requestLogger.Info("Request started")
requestLogger.Debug("Validating input")
requestLogger.Info("Request completed")

// All three logs will include request_id, user_id, and ip
```

### 5. Error Logging

```go
if err := db.Query(ctx, query); err != nil {
    logger.Error("Database query failed",
        logger.Error(err),              // Standard error field
        logger.String("query", query),
        logger.Int("attempts", retries))
    return err
}
```

### 6. Performance Metrics

```go
func (h *Handler) ProcessWebhook(ctx context.Context, webhook *Webhook) error {
    start := time.Now()

    h.logger.Info("Processing webhook",
        logger.String("webhook_id", webhook.ID))

    // ... processing ...

    h.logger.Info("Webhook processed",
        logger.String("webhook_id", webhook.ID),
        logger.Duration("duration", time.Since(start)),
        logger.Int("actions_executed", len(webhook.Actions)))

    return nil
}
```

### 7. Conditional Debug Logging

```go
// Log verbose details only at debug level
if h.debugMode {
    h.logger.Debug("Full request details",
        logger.Any("headers", r.Header),
        logger.String("body", string(body)),
        logger.Any("parsed_data", parsedData))
}

// Or check level explicitly
h.logger.Debug("Processing steps",  // Only logged if level is debug or lower
    logger.String("step", "validation"),
    logger.Any("data", data))
```

### 8. Event-Based Logging

```go
// Define event types as constants
const (
    EventUserLogin    = "user_login"
    EventUserLogout   = "user_logout"
    EventOrderPlaced  = "order_placed"
    EventPaymentFailed = "payment_failed"
)

// Log events with consistent structure
logger.Info("User event",
    logger.String("event_type", EventUserLogin),
    logger.String("user_id", userID),
    logger.Time("timestamp", time.Now()))

// Enables easy filtering in log analysis tools
// Example: grep '"event_type":"user_login"' logs/app.log
```

---

## Advanced Features

### 1. Log Rotation with SIGHUP

The logger supports log file reopening for external log rotation tools (like `logrotate`):

```go
// Setup signal handler in main.go
sigs := make(chan os.Signal, 1)
signal.Notify(sigs, syscall.SIGHUP)

go func() {
    for range sigs {
        appLogger.Info("Received SIGHUP, reopening log files")
        if err := centralLogger.ReopenLogFiles(); err != nil {
            appLogger.Error("Failed to reopen log files", logger.Error(err))
        }
    }
}()
```

**Logrotate configuration** (`/etc/logrotate.d/myapp`):
```
/var/log/myapp/*.log {
    daily
    rotate 7
    compress
    delaycompress
    missingok
    notifempty
    postrotate
        killall -SIGHUP myapp
    endscript
}
```

### 2. Dynamic Log Level Changes

```go
// Change log level at runtime
centralLogger.SetModuleLevel("storage", logger.LogLevelDebug)

// Useful for debugging production issues without restart
```

*Note: This feature would need to be implemented in your fork if required.*

### 3. Log Sampling (High-Volume Reduction)

For high-volume logs (e.g., health checks), implement sampling:

```go
type SampledLogger struct {
    logger logger.Logger
    rate   int  // Log 1 in N messages
    counter int64
}

func (s *SampledLogger) Info(msg string, fields ...logger.Field) {
    count := atomic.AddInt64(&s.counter, 1)
    if count%int64(s.rate) == 0 {
        s.logger.Info(msg, fields...)
    }
}

// Usage
healthLogger := &SampledLogger{
    logger: centralLogger.Module("health"),
    rate:   100,  // Log 1 in 100 health checks
}
```

### 4. Async Logging (Performance)

For high-throughput applications, implement async logging:

```go
type AsyncLogger struct {
    logger logger.Logger
    queue  chan logEntry
}

type logEntry struct {
    level  logger.LogLevel
    msg    string
    fields []logger.Field
}

func NewAsyncLogger(logger logger.Logger, bufferSize int) *AsyncLogger {
    al := &AsyncLogger{
        logger: logger,
        queue:  make(chan logEntry, bufferSize),
    }
    go al.worker()
    return al
}

func (al *AsyncLogger) worker() {
    for entry := range al.queue {
        al.logger.Log(entry.level, entry.msg, entry.fields...)
    }
}

func (al *AsyncLogger) Info(msg string, fields ...logger.Field) {
    select {
    case al.queue <- logEntry{logger.LogLevelInfo, msg, fields}:
    default:
        // Queue full, log synchronously or drop
        al.logger.Warn("Log queue full, dropping message")
    }
}
```

---

## Testing with Mocks

### Option 1: Use a Buffer Logger (Simple)

```go
func TestHandler_Process(t *testing.T) {
    // Create in-memory logger for tests
    buf := &bytes.Buffer{}
    testLogger := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)

    handler := NewHandler(testLogger)

    err := handler.Process(context.Background(), data)
    require.NoError(t, err)

    // Assert log output
    output := buf.String()
    assert.Contains(t, output, "Processing started")
    assert.Contains(t, output, "Processing completed")
}
```

### Option 2: Mock Logger (Advanced)

Generate mocks with `mockery`:

```bash
mockery --name=Logger --dir=pkg/logger --output=mocks
```

Use in tests:

```go
func TestHandler_Process_Error(t *testing.T) {
    mockLogger := mocks.NewMockLogger(t)

    // Expect error log
    mockLogger.EXPECT().
        Error("Processing failed", mock.Anything).
        Once()

    handler := NewHandler(mockLogger)

    err := handler.Process(context.Background(), invalidData)
    require.Error(t, err)

    mockLogger.AssertExpectations(t)
}
```

### Option 3: Discard Logger (Silent Tests)

```go
// Create silent logger for tests that don't care about logs
testLogger := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)

handler := NewHandler(testLogger)
// No log output during tests
```

### Testing Log Output

```go
func TestHandler_LogsCorrectFields(t *testing.T) {
    buf := &bytes.Buffer{}
    testLogger := logger.NewSlogLogger(buf, logger.LogLevelInfo, time.UTC)

    handler := NewHandler(testLogger)
    handler.Process(context.Background(), data)

    // Parse JSON log output
    var logEntry map[string]interface{}
    err := json.Unmarshal(buf.Bytes(), &logEntry)
    require.NoError(t, err)

    // Assert specific fields
    assert.Equal(t, "Processing completed", logEntry["msg"])
    assert.Equal(t, "data-123", logEntry["data_id"])
    assert.NotEmpty(t, logEntry["duration"])
}
```

---

## Best Practices

### 1. Always Inject Logger Interface

```go
// ✅ GOOD - Testable
type Handler struct {
    logger logger.Logger  // Interface
}

func NewHandler(log logger.Logger) *Handler {
    return &Handler{logger: log}
}

// ❌ BAD - Not testable
type Handler struct {
    logger *logger.SlogLogger  // Concrete type
}
```

### 2. Use Module Scoping

```go
// ✅ GOOD - Clear log source
appLogger := centralLogger.Module("birdnet")
datastoreLogger := centralLogger.Module("datastore")
authLogger := centralLogger.Module("auth")

storage := NewStorage(datastoreLogger)
auth := NewAuth(authLogger)

// ❌ BAD - All logs from "main" module
storage := NewStorage(appLogger)
auth := NewAuth(appLogger)
```

### 3. Use Structured Fields, Not String Concatenation

```go
// ✅ GOOD - Structured, queryable
logger.Info("User logged in",
    logger.String("user_id", userID),
    logger.String("ip", clientIP))

// ❌ BAD - Hard to parse, not queryable
logger.Info(fmt.Sprintf("User %s logged in from %s", userID, clientIP))
```

### 4. Log Errors with Context

```go
// ✅ GOOD - Rich context
if err := db.Save(ctx, user); err != nil {
    logger.Error("Failed to save user",
        logger.Error(err),
        logger.String("user_id", user.ID),
        logger.String("operation", "save"),
        logger.String("table", "users"))
    return err
}

// ❌ BAD - Minimal context
if err := db.Save(ctx, user); err != nil {
    logger.Error("Save failed", logger.Error(err))
    return err
}
```

### 5. Choose Appropriate Log Levels

```go
// TRACE - Very verbose, rarely used
logger.Trace("Entering function", logger.String("func", "ProcessData"))

// DEBUG - Detailed diagnostic info
logger.Debug("SQL query executed",
    logger.String("query", query),
    logger.Int("rows", rows))

// INFO - Normal operational events
logger.Info("Server started",
    logger.String("address", addr),
    logger.Int("port", port))

// WARN - Unexpected but recoverable
logger.Warn("Request took too long",
    logger.Duration("duration", elapsed),
    logger.Duration("threshold", maxDuration))

// ERROR - Errors requiring attention
logger.Error("Database connection failed",
    logger.Error(err),
    logger.String("host", host))
```

### 6. Use Constants for Event Types

```go
const (
    EventUserLogin    = "user_login"
    EventDataProcessed = "data_processed"
    EventPaymentFailed = "payment_failed"
)

logger.Info("User event",
    logger.String("event_type", EventUserLogin),
    logger.String("user_id", userID))

// Enables filtering: grep '"event_type":"user_login"' logs/*.log
```

### 7. Include Timing Metrics

```go
func (h *Handler) Process(ctx context.Context, data *Data) error {
    start := time.Now()
    defer func() {
        h.logger.Info("Processing completed",
            logger.String("data_id", data.ID),
            logger.Duration("duration", time.Since(start)))
    }()

    // ... processing ...
}
```

### 8. Validate Logger is Not Nil

```go
func NewHandler(logger logger.Logger) (*Handler, error) {
    if logger == nil {
        return nil, errors.New("logger is required")
    }
    return &Handler{logger: logger}, nil
}

// Defensive: Methods should handle nil logger gracefully
func (h *Handler) Process() {
    if h.logger != nil {
        h.logger.Info("Processing")
    }
    // ... continue processing
}
```

### 9. Flush on Shutdown

```go
func main() {
    centralLogger, err := logger.NewCentralLogger(&cfg.Logging)
    if err != nil {
        log.Fatal(err)
    }

    // Ensure logs are written on shutdown
    defer func() {
        if err := centralLogger.Flush(); err != nil {
            log.Printf("Failed to flush logs: %v", err)
        }
        if err := centralLogger.Close(); err != nil {
            log.Printf("Failed to close logger: %v", err)
        }
    }()

    // ... application code
}
```

### 10. Don't Log Sensitive Data

```go
// ✅ GOOD - Redacted sensitive data
logger.Info("User authenticated",
    logger.String("user_id", user.ID),
    logger.String("email_hash", hashEmail(user.Email)))

// ❌ BAD - Exposes PII
logger.Info("User authenticated",
    logger.String("user_id", user.ID),
    logger.String("email", user.Email),
    logger.String("password", user.Password))  // NEVER log passwords!
```

---

## Migration Checklist

If migrating from another logging system:

- [ ] Copy logger package files to your project
- [ ] Update import paths
- [ ] Add LoggingConfig to your config structure
- [ ] Add logging section to config.yaml
- [ ] Update main.go to initialize CentralLogger
- [ ] Create module-scoped loggers for each package
- [ ] Update component constructors to accept Logger interface
- [ ] Replace old log calls with structured logging
- [ ] Add nil checks for injected loggers
- [ ] Update tests to inject mock or buffer loggers
- [ ] Add log file rotation (logrotate or SIGHUP handler)
- [ ] Test different log levels and outputs
- [ ] Document module names and log events for your team

---

## Troubleshooting

### Logs Not Appearing

1. Check log level configuration (might be too high)
2. Verify console/file output is enabled in config
3. Check file permissions for log directory
4. Ensure logger is not nil when calling methods
5. Call `Flush()` before exiting

### Logs Missing Fields

1. Ensure using structured fields, not string concatenation
2. Check if fields are being accumulated with `With()`
3. Verify module name is set correctly
4. Check if context contains expected values (trace_id)

### Performance Issues

1. Reduce log level in hot paths
2. Implement sampling for high-volume logs
3. Use async logging for high-throughput
4. Check if file I/O is blocking (use buffered writers)

### File Rotation Not Working

1. Verify SIGHUP handler is registered
2. Check logrotate configuration (test with `logrotate -d`)
3. Ensure application has permission to reopen files
4. Test `ReopenLogFile()` method manually

---

## Additional Resources

### Files to Review

- `pkg/logger/logger.go` - Interface and field constructors
- `pkg/logger/slog_logger.go` - Core implementation
- `pkg/logger/central_logger.go` - Module routing
- `pkg/logger/config.go` - Configuration structures
- `pkg/logger/logger_test.go` - Usage examples and tests
- `cmd/hookrelay/main.go` - Initialization example
- `internal/server/webhook_handler.go` - Real-world usage

### Further Enhancements

Consider implementing:

1. **Sampling**: Reduce high-volume logs (1 in N)
2. **Buffering**: Async logging for performance
3. **Metrics**: Count log events by level/module
4. **Alerts**: Send critical errors to monitoring systems
5. **Redaction**: Automatically sanitize sensitive fields
6. **Compression**: Compress log files in real-time
7. **Remote Logging**: Send to centralized log aggregation (Elasticsearch, Loki)
8. **Structured Filters**: Filter logs by field values, not just level
9. **Dynamic Config**: Change log levels without restart
10. **Log Aggregation**: Parse JSON logs with tools like `jq`, Splunk, or DataDog

---

## Summary

The HookRelay logging system provides:

✅ **Interface-based design** for dependency injection and testing
✅ **Module-aware routing** for organized logs
✅ **Structured logging** with type-safe fields
✅ **Flexible configuration** (console, file, per-module)
✅ **Context propagation** for request tracing
✅ **Built on `log/slog`** for standard library compatibility
✅ **Production-ready** with log rotation support

By following this guide, you can implement the same robust logging system in your Go projects, ensuring consistent, queryable, and maintainable logs across your application.
