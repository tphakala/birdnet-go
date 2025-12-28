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
	handler    slog.Handler
	slogLogger *slog.Logger // cached instance to avoid per-call allocation
	level      slog.Level
	module     string
	timezone   *time.Location
	fields     []Field
	logWriter  *BufferedFileWriter
	filePath   string
	mu         sync.RWMutex // protects logWriter and slogLogger
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
		handler:    handler,
		slogLogger: slog.New(handler), // cache logger instance
		level:      parseSlogLevel(level),
		timezone:   timezone,
		fields:     nil, // nil is equivalent to empty slice but avoids allocation
	}
}

// NewConsoleLogger creates a console logger with human-readable text format.
// Use this for bootstrap/fallback scenarios before the central logger is initialized.
// Output format matches CentralLogger console output: LEVEL  [module] message key=value
// (Timestamps are omitted - journald/Docker adds them automatically)
func NewConsoleLogger(module string, level LogLevel) *SlogLogger {
	tz := time.Local
	handler := newTextHandler(os.Stdout, parseSlogLevel(level), tz)

	return &SlogLogger{
		handler:    handler,
		slogLogger: slog.New(handler), // cache logger instance
		level:      parseSlogLevel(level),
		module:     module,
		timezone:   tz,
		fields:     nil, // nil is equivalent to empty slice but avoids allocation
	}
}

// NewSlogLoggerWithFile creates a new slog-based logger with buffered file output
func NewSlogLoggerWithFile(filePath string, level LogLevel, timezone *time.Location) (*SlogLogger, error) {
	if timezone == nil {
		timezone = time.UTC
	}

	// Create buffered writer for log file
	writer, err := NewBufferedFileWriter(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log writer: %w", err)
	}

	// Create handler with buffered writer
	opts := &slog.HandlerOptions{
		Level: parseSlogLevel(level),
	}
	handler := slog.NewJSONHandler(writer, opts)

	logger := &SlogLogger{
		handler:    handler,
		slogLogger: slog.New(handler), // cache logger instance
		level:      parseSlogLevel(level),
		timezone:   timezone,
		fields:     nil, // nil is equivalent to empty slice but avoids allocation
		logWriter:  writer,
		filePath:   filePath,
	}

	return logger, nil
}

// ReopenLogFile reopens the log file (for log rotation via SIGHUP).
// Note: With buffered writers, this closes the old writer and creates a new one.
func (l *SlogLogger) ReopenLogFile() error {
	if l.filePath == "" {
		return nil // not using file logging
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Close existing writer if open
	if l.logWriter != nil {
		if err := l.logWriter.Close(); err != nil {
			return fmt.Errorf("failed to close existing log writer: %w", err)
		}
	}

	// Create new buffered writer
	writer, err := NewBufferedFileWriter(l.filePath)
	if err != nil {
		return fmt.Errorf("failed to create new log writer: %w", err)
	}
	l.logWriter = writer

	// Recreate handler with new writer
	opts := &slog.HandlerOptions{
		Level: l.level,
	}
	l.handler = slog.NewJSONHandler(writer, opts)
	l.slogLogger = slog.New(l.handler) // update cached logger

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
		handler:    l.handler,
		slogLogger: l.slogLogger, // share cached logger instance
		level:      l.level,
		module:     moduleName,
		timezone:   l.timezone,
		fields:     l.fields,
		logWriter:  l.logWriter,
		filePath:   l.filePath,
	}
}

// Trace logs a trace message (most verbose level)
func (l *SlogLogger) Trace(msg string, fields ...Field) {
	if l == nil {
		return
	}
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
		handler:    l.handler,
		slogLogger: l.slogLogger, // share cached logger instance
		level:      l.level,
		module:     l.module,
		timezone:   l.timezone,
		fields:     slices.Concat(l.fields, fields),
		logWriter:  l.logWriter,
		filePath:   l.filePath,
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

	// Extract trace ID from context if available
	// Check first to avoid allocation when no trace ID exists
	traceID := getTraceIDFromContext(ctx)
	if traceID == "" {
		return l
	}

	return l.With(String(traceIDKey, traceID))
}

// Flush ensures all buffered logs are written to OS buffers
func (l *SlogLogger) Flush() error {
	if l == nil {
		return nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	// Flush buffered writer if we're writing to one
	if l.logWriter != nil {
		if err := l.logWriter.Flush(); err != nil {
			return fmt.Errorf("failed to flush log writer: %w", err)
		}
	}

	return nil
}

// Close closes the buffered writer and underlying file
func (l *SlogLogger) Close() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logWriter != nil {
		if err := l.logWriter.Close(); err != nil {
			return fmt.Errorf("failed to close log writer: %w", err)
		}
		l.logWriter = nil
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
		attrs = append(attrs, slog.String(moduleKey, l.module))
	}

	// Add accumulated context fields
	for i := range l.fields {
		attrs = append(attrs, fieldToAttr(l.fields[i]))
	}

	// Add current fields
	for i := range fields {
		attrs = append(attrs, fieldToAttr(fields[i]))
	}

	// Use cached logger instance (avoids allocation per log call)
	l.slogLogger.LogAttrs(context.Background(), level, msg, attrs...)

	// Return slice to pool
	*attrsPtr = attrs
	putAttrs(attrsPtr)
}

// parseSlogLevel converts LogLevel to slog.Level
func parseSlogLevel(level LogLevel) slog.Level {
	switch level {
	case LogLevelTrace:
		return traceLevelValue
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
