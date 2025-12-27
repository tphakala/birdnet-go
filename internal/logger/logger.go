// Package logger provides a structured, module-aware logging system built on Go's standard log/slog.
//
// # Features
//
//   - Interface-based design for dependency injection and testing
//   - Module-scoped loggers for hierarchical organization (e.g., "main", "storage", "auth")
//   - Structured logging with type-safe field constructors
//   - Flexible output routing (console, files, per-module files)
//   - Context-aware logging with automatic trace ID extraction
//   - YAML-based configuration
//   - Log rotation support via SIGHUP
//   - Zero external dependencies (uses only Go standard library)
//
// # Quick Start
//
// Basic usage:
//
//	cfg := &logger.LoggingConfig{
//	    DefaultLevel: "info",
//	    Timezone:     "UTC",
//	    Console: &logger.ConsoleOutput{
//	        Enabled: true,
//	        Level:   "info",
//	    },
//	}
//
//	centralLogger, err := logger.NewCentralLogger(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer centralLogger.Close()
//
//	// Create module-scoped logger
//	appLogger := centralLogger.Module("main")
//
//	// Use structured logging
//	appLogger.Info("Application started",
//	    logger.String("version", "1.0.0"),
//	    logger.Int("workers", 10))
//
// # Module Scoping
//
// Create hierarchical loggers for different components:
//
//	storageLogger := centralLogger.Module("storage")
//	sqliteLogger := storageLogger.Module("sqlite")
//	sqliteLogger.Debug("Query executed")  // Output includes: module="storage.sqlite"
//
// # Context-Aware Logging
//
// Automatically extract trace IDs from context:
//
//	ctx := context.WithValue(ctx, "trace_id", "abc-123")
//	contextLogger := appLogger.WithContext(ctx)
//	contextLogger.Info("Processing request")  // Includes trace_id automatically
//
// # Field Accumulation
//
// Build loggers with persistent fields:
//
//	requestLogger := appLogger.With(
//	    logger.String("request_id", "req-123"),
//	    logger.String("user_id", "user-456"))
//
//	requestLogger.Info("Started")    // Both logs include request_id and user_id
//	requestLogger.Info("Completed")
//
// # Dependency Injection
//
// Always inject Logger interface into components:
//
//	type Handler struct {
//	    logger logger.Logger
//	}
//
//	func NewHandler(log logger.Logger) (*Handler, error) {
//	    if log == nil {
//	        return nil, errors.New("logger is required")
//	    }
//	    return &Handler{logger: log}, nil
//	}
//
// # Testing
//
// Use buffer or discard logger for tests:
//
//	// Buffer logger (check output)
//	buf := &bytes.Buffer{}
//	testLogger := logger.NewSlogLogger(buf, logger.LogLevelDebug, time.UTC)
//
//	// Discard logger (silent tests)
//	testLogger := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)
//
// # Configuration
//
// Configure via YAML:
//
//	logging:
//	  default_level: "info"
//	  timezone: "UTC"
//	  console:
//	    enabled: true
//	    level: "info"
//	  file_output:
//	    enabled: true
//	    path: "logs/app.log"
//	    level: "debug"
//	  module_levels:
//	    storage: "debug"
//	  modules:
//	    auth:
//	      enabled: true
//	      file_path: "logs/auth.log"
//	      level: "info"
//
// # Log Levels
//
// Available log levels from most to least verbose:
//
//   - Trace: Very detailed debugging information (rarely used)
//   - Debug: Detailed diagnostic information
//   - Info: Normal operational messages (default)
//   - Warn: Warning messages for unexpected but recoverable events
//   - Error: Error messages requiring attention
//
// # Field Types
//
// Type-safe field constructors for structured logging:
//
//	logger.String("key", "value")              // String field
//	logger.Int("count", 42)                    // Integer field
//	logger.Int64("bignum", 123456789)          // 64-bit integer
//	logger.Bool("enabled", true)               // Boolean field
//	logger.Error(err)                          // Error field (key is always "error")
//	logger.Duration("elapsed", 5*time.Second)  // Duration field
//	logger.Time("timestamp", time.Now())       // Time field
//	logger.Any("data", complexStruct)          // Any value (JSON)
//
// # Performance Considerations
//
// The logger is designed for production use with minimal overhead:
//
//   - Log level checks happen before field evaluation
//   - Structured fields avoid string concatenation
//   - File I/O is buffered and can be flushed explicitly
//   - Module routing is cached per logger instance
//
// # Log Rotation
//
// Support external log rotation tools via SIGHUP:
//
//	sigs := make(chan os.Signal, 1)
//	signal.Notify(sigs, syscall.SIGHUP)
//	go func() {
//	    for range sigs {
//	        logger.ReopenLogFile()
//	    }
//	}()
//
// # Thread Safety
//
// All logger implementations are thread-safe and can be safely used from multiple goroutines.
//
// # Output Format
//
// Logs are output as JSON for machine parsing:
//
//	{"time":"2025-01-12T10:30:00Z","level":"INFO","msg":"User logged in","module":"auth","user_id":"123"}
//
// # Best Practices
//
//   - Always inject Logger interface, never concrete types
//   - Use module scoping to identify log sources
//   - Use structured fields, not string concatenation
//   - Log errors with context (operation, IDs, state)
//   - Call Flush() and Close() on shutdown
//   - Don't log sensitive data (passwords, tokens, PII)
//   - Use appropriate log levels for each message
//
// # See Also
//
// For more information, see:
//   - LOGGING_IMPLEMENTATION_GUIDE.md for detailed usage patterns
//   - LOGGING_QUICK_REFERENCE.md for syntax quick reference
//   - LOGGER_EXTRACTION_GUIDE.md for reusing in other projects
package logger

import (
	"context"
	"time"
	"unique"
)

// LogLevel represents log severity levels
type LogLevel string

const (
	LogLevelTrace LogLevel = "trace"
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// Field represents a structured log field.
// Keys are interned using unique.Make() for memory efficiency - the same key
// string (e.g., "error", "cve_id") used across millions of log calls shares
// a single allocation.
type Field struct {
	Key   string
	Value any
}

// internKey returns an interned version of the key string.
// This ensures repeated keys share the same underlying memory.
func internKey(key string) string {
	return unique.Make(key).Value()
}

// Pre-interned common keys for zero-allocation access
var (
	errorKey = internKey("error")
)

// Logger is the centralized logging interface for dependency injection
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

// Field Constructors
//
// The following functions create type-safe field values for structured logging.
// Use these instead of string concatenation or formatting to ensure logs are
// machine-parseable and queryable.
//
// Example usage:
//
//	log.Info("User action",
//	    logger.String("user_id", "123"),
//	    logger.String("action", "login"),
//	    logger.Int("attempt", 1),
//	    logger.Bool("success", true))

// String creates a string field for structured logging.
//
// Use this for text values like IDs, names, statuses, etc.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Request processed",
//	    logger.String("request_id", "req-123"),
//	    logger.String("method", "POST"),
//	    logger.String("endpoint", "/api/users"))
func String(key, value string) Field {
	return Field{Key: internKey(key), Value: value}
}

// Int creates an integer field for structured logging.
//
// Use this for counts, sizes, port numbers, status codes, etc.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Processing batch",
//	    logger.Int("batch_size", 100),
//	    logger.Int("processed", 95),
//	    logger.Int("failed", 5))
func Int(key string, value int) Field {
	return Field{Key: internKey(key), Value: value}
}

// Int64 creates a 64-bit integer field for structured logging.
//
// Use this for large numbers that don't fit in 32-bit int.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Database stats",
//	    logger.Int64("total_records", 5000000000),
//	    logger.Int64("bytes_processed", fileSize))
func Int64(key string, value int64) Field {
	return Field{Key: internKey(key), Value: value}
}

// Uint64 creates an unsigned 64-bit integer field for structured logging.
//
// Use this for file sizes, byte counts, and other unsigned large numbers.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Disk usage",
//	    logger.Uint64("total_bytes", diskInfo.TotalBytes),
//	    logger.Uint64("used_bytes", diskInfo.UsedBytes))
func Uint64(key string, value uint64) Field {
	return Field{Key: internKey(key), Value: value}
}

// Float32 creates a 32-bit float field for structured logging.
//
// Use this for decimal numbers, percentages, confidence scores, etc.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Detection result",
//	    logger.String("species", species),
//	    logger.Float32("confidence", 0.95))
func Float32(key string, value float32) Field {
	return Field{Key: internKey(key), Value: value}
}

// Float64 creates a 64-bit float field for structured logging.
//
// Use this for decimal numbers requiring high precision.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Measurement",
//	    logger.Float64("temperature", 23.456789),
//	    logger.Float64("humidity", 65.2))
func Float64(key string, value float64) Field {
	return Field{Key: internKey(key), Value: value}
}

// Bool creates a boolean field for structured logging.
//
// Use this for flags, states, success/failure indicators.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Feature check",
//	    logger.Bool("feature_enabled", true),
//	    logger.Bool("authenticated", user.IsAuth),
//	    logger.Bool("cached", fromCache))
func Bool(key string, value bool) Field {
	return Field{Key: internKey(key), Value: value}
}

// Error creates an error field for structured logging.
//
// The field key is always "error" (pre-interned for zero allocation).
// If err is nil, the value will be nil.
// Use this to log errors with additional context fields.
//
// Example:
//
//	if err := saveUser(user); err != nil {
//	    log.Error("Failed to save user",
//	        logger.Error(err),
//	        logger.String("user_id", user.ID),
//	        logger.String("operation", "save"))
//	    return err
//	}
func Error(err error) Field {
	if err == nil {
		return Field{Key: errorKey, Value: nil}
	}
	return Field{Key: errorKey, Value: err.Error()}
}

// Duration creates a duration field for structured logging.
//
// Use this for elapsed time, timeouts, latencies, etc.
// The duration is converted to a string representation (e.g., "1.5s", "200ms").
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	start := time.Now()
//	// ... do work ...
//	log.Info("Operation completed",
//	    logger.String("operation", "process_data"),
//	    logger.Duration("elapsed", time.Since(start)),
//	    logger.Duration("timeout", 30*time.Second))
func Duration(key string, value time.Duration) Field {
	return Field{Key: internKey(key), Value: value.String()}
}

// Time creates a time field for structured logging.
//
// Use this for timestamps, deadlines, scheduled times, etc.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Info("Event scheduled",
//	    logger.String("event_id", event.ID),
//	    logger.Time("scheduled_at", event.ScheduledTime),
//	    logger.Time("created_at", event.CreatedAt))
func Time(key string, value time.Time) Field {
	return Field{Key: internKey(key), Value: value}
}

// Any creates a field with any value for structured logging.
//
// Use this for complex types that will be serialized to JSON.
// Avoid using this for simple types - prefer the type-specific constructors instead.
// The key is interned for memory efficiency across repeated log calls.
//
// Example:
//
//	log.Debug("Request details",
//	    logger.String("request_id", reqID),
//	    logger.Any("headers", req.Header),
//	    logger.Any("query_params", req.URL.Query()))
//
// Warning: Ensure the value is JSON-serializable or it may cause logging errors.
func Any(key string, value any) Field {
	return Field{Key: internKey(key), Value: value}
}
