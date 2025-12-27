package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"
)

const (
	// LogFilePermissions is the default file permissions for log files (rw-------)
	LogFilePermissions = 0o600
)

// SlogLogger implements Logger interface using Go's standard log/slog
type SlogLogger struct {
	handler  slog.Handler
	level    slog.Level
	module   string
	timezone *time.Location
	fields   []Field
	logFile  *os.File
	filePath string
	mu       sync.RWMutex // protects logFile
}

// NewSlogLogger creates a new slog-based logger with JSON output
func NewSlogLogger(writer io.Writer, level LogLevel, timezone *time.Location) *SlogLogger {
	if writer == nil {
		writer = os.Stdout
	}
	if timezone == nil {
		timezone = time.UTC
	}

	opts := &slog.HandlerOptions{
		Level: parseSlogLevel(level),
	}

	handler := slog.NewJSONHandler(writer, opts)

	return &SlogLogger{
		handler:  handler,
		level:    parseSlogLevel(level),
		timezone: timezone,
		fields:   make([]Field, 0),
	}
}

// NewConsoleLogger creates a console logger with human-readable text format.
// Use this for bootstrap/fallback scenarios before the central logger is initialized.
// Output format matches CentralLogger console output: [DD.MM.YYYY HH:MM:SS] LEVEL  [module] message key=value
func NewConsoleLogger(module string, level LogLevel) *SlogLogger {
	tz := time.Local

	return &SlogLogger{
		handler:  newTextHandler(os.Stdout, parseSlogLevel(level), tz),
		level:    parseSlogLevel(level),
		module:   module,
		timezone: tz,
		fields:   make([]Field, 0),
	}
}

// NewSlogLoggerWithFile creates a new slog-based logger with file output
func NewSlogLoggerWithFile(filePath string, level LogLevel, timezone *time.Location) (*SlogLogger, error) {
	if timezone == nil {
		timezone = time.UTC
	}

	logger := &SlogLogger{
		level:    parseSlogLevel(level),
		timezone: timezone,
		fields:   make([]Field, 0),
		filePath: filePath,
	}

	// Open log file
	if err := logger.openLogFile(); err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Create handler with file writer
	opts := &slog.HandlerOptions{
		Level: parseSlogLevel(level),
	}
	logger.handler = slog.NewJSONHandler(logger.logFile, opts)

	return logger, nil
}

// openLogFile opens or reopens the log file
func (l *SlogLogger) openLogFile() error {
	if l.filePath == "" {
		return fmt.Errorf("log file path not set")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Close existing file if open
	if l.logFile != nil {
		if err := l.logFile.Close(); err != nil {
			return fmt.Errorf("failed to close existing log file: %w", err)
		}
	}

	// Open file with append mode
	file, err := os.OpenFile(l.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LogFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to open log file %s: %w", l.filePath, err)
	}

	l.logFile = file
	return nil
}

// ReopenLogFile reopens the log file (for log rotation via SIGHUP)
func (l *SlogLogger) ReopenLogFile() error {
	if l.filePath == "" {
		return nil // not using file logging
	}

	// Reopen the file
	if err := l.openLogFile(); err != nil {
		return err
	}

	// Recreate handler with new file handle
	opts := &slog.HandlerOptions{
		Level: l.level,
	}

	l.mu.Lock()
	l.handler = slog.NewJSONHandler(l.logFile, opts)
	l.mu.Unlock()

	return nil
}

// Module returns a logger scoped to a specific module
func (l *SlogLogger) Module(name string) Logger {
	if l == nil {
		return nil
	}

	moduleName := name
	if l.module != "" {
		moduleName = l.module + "." + name
	}

	return &SlogLogger{
		handler:  l.handler,
		level:    l.level,
		module:   moduleName,
		timezone: l.timezone,
		fields:   l.fields,
		logFile:  l.logFile,
		filePath: l.filePath,
	}
}

// Trace logs a trace message (most verbose level)
func (l *SlogLogger) Trace(msg string, fields ...Field) {
	if l == nil {
		return
	}
	// Trace level is -8 (below Debug which is -4)
	const traceLevelValue = slog.Level(-8)
	if l.level > traceLevelValue {
		return
	}
	l.log(traceLevelValue, msg, fields...)
}

// Debug logs a debug message
func (l *SlogLogger) Debug(msg string, fields ...Field) {
	if l == nil {
		return
	}
	if l.level > slog.LevelDebug {
		return
	}
	l.log(slog.LevelDebug, msg, fields...)
}

// Info logs an info message
func (l *SlogLogger) Info(msg string, fields ...Field) {
	if l == nil {
		return
	}
	if l.level > slog.LevelInfo {
		return
	}
	l.log(slog.LevelInfo, msg, fields...)
}

// Warn logs a warning message
func (l *SlogLogger) Warn(msg string, fields ...Field) {
	if l == nil {
		return
	}
	if l.level > slog.LevelWarn {
		return
	}
	l.log(slog.LevelWarn, msg, fields...)
}

// Error logs an error message
func (l *SlogLogger) Error(msg string, fields ...Field) {
	if l == nil {
		return
	}
	l.log(slog.LevelError, msg, fields...)
}

// Log logs a message with explicit level
func (l *SlogLogger) Log(level LogLevel, msg string, fields ...Field) {
	if l == nil {
		return
	}
	l.log(parseSlogLevel(level), msg, fields...)
}

// With returns a new logger with accumulated fields
func (l *SlogLogger) With(fields ...Field) Logger {
	if l == nil {
		return nil
	}

	return &SlogLogger{
		handler:  l.handler,
		level:    l.level,
		module:   l.module,
		timezone: l.timezone,
		fields:   slices.Concat(l.fields, fields),
		logFile:  l.logFile,
		filePath: l.filePath,
	}
}

// WithContext returns a logger with context values
func (l *SlogLogger) WithContext(ctx context.Context) Logger {
	if l == nil {
		return nil
	}
	if ctx == nil {
		return l
	}

	fields := make([]Field, 0, 2) //nolint:mnd // Capacity hint for trace_id field

	// Extract trace ID from context if available
	if traceID := getTraceID(ctx); traceID != "" {
		fields = append(fields, String("trace_id", traceID))
	}

	if len(fields) == 0 {
		return l
	}

	return l.With(fields...)
}

// Flush ensures all buffered logs are written
func (l *SlogLogger) Flush() error {
	if l == nil {
		return nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	// Sync file if we're writing to one
	if l.logFile != nil {
		if err := l.logFile.Sync(); err != nil {
			return fmt.Errorf("failed to sync log file: %w", err)
		}
	}

	return nil
}

// Close closes the log file if open
func (l *SlogLogger) Close() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logFile != nil {
		if err := l.logFile.Close(); err != nil {
			return fmt.Errorf("failed to close log file: %w", err)
		}
		l.logFile = nil
	}

	return nil
}

// log is the internal logging method
func (l *SlogLogger) log(level slog.Level, msg string, fields ...Field) {
	if l == nil {
		return
	}

	// Get attribute slice from pool (reduces allocations in hot path)
	attrsPtr := getAttrs()
	attrs := *attrsPtr

	// Add module if set
	if l.module != "" {
		attrs = append(attrs, slog.String("module", l.module))
	}

	// Add accumulated context fields
	for _, f := range l.fields {
		attrs = append(attrs, l.fieldToAttr(f))
	}

	// Add current fields
	for _, f := range fields {
		attrs = append(attrs, l.fieldToAttr(f))
	}

	// Create logger from handler and log
	logger := slog.New(l.handler)
	logger.LogAttrs(context.Background(), level, msg, attrs...)

	// Return slice to pool
	*attrsPtr = attrs
	putAttrs(attrsPtr)
}

// fieldToAttr converts Field to slog.Attr
func (l *SlogLogger) fieldToAttr(f Field) slog.Attr {
	switch v := f.Value.(type) {
	case string:
		return slog.String(f.Key, v)
	case int:
		return slog.Int(f.Key, v)
	case int64:
		return slog.Int64(f.Key, v)
	case bool:
		return slog.Bool(f.Key, v)
	case time.Time:
		return slog.Time(f.Key, v)
	case time.Duration:
		return slog.Duration(f.Key, v)
	default:
		return slog.Any(f.Key, v)
	}
}

// parseSlogLevel converts LogLevel to slog.Level
func parseSlogLevel(level LogLevel) slog.Level {
	switch level {
	case LogLevelTrace:
		return slog.Level(-8) // Trace level below Debug (-4)
	case LogLevelDebug:
		return slog.LevelDebug
	case LogLevelInfo:
		return slog.LevelInfo
	case LogLevelWarn:
		return slog.LevelWarn
	case LogLevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// getTraceID extracts trace ID from context
func getTraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	// Try typed key first (preferred)
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		return traceID
	}
	// Fall back to string key for backward compatibility
	if traceID, ok := ctx.Value("trace_id").(string); ok {
		return traceID
	}
	return ""
}
