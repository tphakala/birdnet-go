package logger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Default capacity for pooled attribute slices (module + ~7 fields)
const defaultAttrCapacity = 8

// loggerContextKey is a typed key for context values to avoid string collisions.
// Using a struct type ensures our keys won't collide with other packages' string keys.
type loggerContextKey struct{ name string }

// TraceIDKey is the context key for trace IDs. Use WithTraceID() to set values.
var TraceIDKey = loggerContextKey{"trace_id"}

// WithTraceID returns a new context with the trace ID set
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// attrPool provides reusable slices for slog.Attr to reduce allocations in hot paths.
// Each log call would otherwise allocate a new slice; pooling eliminates this overhead.
var attrPool = sync.Pool{
	New: func() any {
		// Pre-allocate capacity for typical log calls
		s := make([]slog.Attr, 0, defaultAttrCapacity)
		return &s
	},
}

// getAttrs retrieves an attribute slice from the pool
func getAttrs() *[]slog.Attr {
	ptr, ok := attrPool.Get().(*[]slog.Attr)
	if !ok {
		s := make([]slog.Attr, 0, defaultAttrCapacity)
		return &s
	}
	return ptr
}

// putAttrs returns an attribute slice to the pool after resetting it
func putAttrs(attrs *[]slog.Attr) {
	*attrs = (*attrs)[:0] // Reset length, keep capacity
	attrPool.Put(attrs)
}

// CentralLogger manages module-aware logging with flexible routing
type CentralLogger struct {
	config       *LoggingConfig
	timezone     *time.Location
	baseHandler  slog.Handler          // Default handler for modules without specific config
	moduleFiles  map[string]*os.File   // Per-module file handles
	moduleLevels map[string]slog.Level // Per-module log levels
	mu           sync.RWMutex          // Protects concurrent access
}

// NewCentralLogger creates a centralized logger with module routing
func NewCentralLogger(cfg *LoggingConfig) (*CentralLogger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("logging config cannot be nil")
	}

	// Load timezone
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %s: %w", cfg.Timezone, err)
	}

	cl := &CentralLogger{
		config:       cfg,
		timezone:     tz,
		moduleFiles:  make(map[string]*os.File),
		moduleLevels: make(map[string]slog.Level),
	}

	// Parse module levels
	for module, levelStr := range cfg.ModuleLevels {
		cl.moduleLevels[module] = parseLogLevel(levelStr)
	}

	// Create base handler (console and/or main file)
	if err := cl.createBaseHandler(); err != nil {
		return nil, fmt.Errorf("failed to create base handler: %w", err)
	}

	// Open module-specific log files
	for module, moduleConfig := range cfg.ModuleOutputs {
		if !moduleConfig.Enabled {
			continue
		}

		// Ensure directory exists
		if err := ensureFileDirectory(moduleConfig.FilePath); err != nil {
			return nil, fmt.Errorf("failed to create directory for module %s: %w", module, err)
		}

		// Open module log file
		file, err := os.OpenFile(moduleConfig.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LogFilePermissions)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file for module %s: %w", module, err)
		}
		cl.moduleFiles[module] = file
	}

	return cl, nil
}

// createBaseHandler creates the default handler for console and/or main file output
func (cl *CentralLogger) createBaseHandler() error {
	var handlers []slog.Handler

	// Console handler - USE TEXT FORMAT for human-readable output
	if cl.config.Console != nil && cl.config.Console.Enabled {
		consoleLevel := parseLogLevel(cl.config.Console.Level)
		handlers = append(handlers, newTextHandler(os.Stdout, consoleLevel, cl.timezone))
	}

	// Main file handler - USE JSON FORMAT for machine parsing
	if cl.config.FileOutput != nil && cl.config.FileOutput.Enabled {
		// Ensure directory exists
		if err := ensureFileDirectory(cl.config.FileOutput.Path); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open main log file
		file, err := os.OpenFile(cl.config.FileOutput.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, LogFilePermissions)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}

		fileLevel := parseLogLevel(cl.config.FileOutput.Level)
		opts := &slog.HandlerOptions{
			Level: fileLevel,
		}
		handlers = append(handlers, slog.NewJSONHandler(file, opts))
	}

	if len(handlers) == 0 {
		// Fallback to stdout text handler if nothing is configured
		handlers = append(handlers, newTextHandler(os.Stdout, parseLogLevel(cl.config.DefaultLevel), cl.timezone))
	}

	if len(handlers) == 1 {
		cl.baseHandler = handlers[0]
	} else {
		cl.baseHandler = newMultiWriterHandler(handlers...)
	}

	return nil
}

// Module returns a logger scoped to a specific module
func (cl *CentralLogger) Module(name string) Logger {
	if cl == nil {
		return nil
	}

	cl.mu.RLock()
	defer cl.mu.RUnlock()

	// Get module-specific configuration
	moduleConfig, hasModuleConfig := cl.config.ModuleOutputs[name]
	moduleLevel := cl.getModuleLevelLocked(name)

	// Override module level if explicitly configured in ModuleOutputs
	// This ensures both the handler and the moduleLogger use the same level
	if hasModuleConfig && moduleConfig.Level != "" {
		moduleLevel = parseLogLevel(moduleConfig.Level)
	}

	// Build handlers for this module
	var handlers []slog.Handler

	// Add module-specific file handler if configured
	if hasModuleConfig && moduleConfig.Enabled {
		if moduleFile, ok := cl.moduleFiles[name]; ok {
			opts := &slog.HandlerOptions{
				Level: moduleLevel,
			}
			handlers = append(handlers, slog.NewJSONHandler(moduleFile, opts))
		}

		// Also log to console if requested - USE TEXT FORMAT
		if moduleConfig.ConsoleAlso && cl.config.Console != nil && cl.config.Console.Enabled {
			handlers = append(handlers, newTextHandler(os.Stdout, moduleLevel, cl.timezone))
		}
	} else {
		// Use base handler (console + main file)
		handlers = append(handlers, cl.baseHandler)
	}

	// Create handler
	var handler slog.Handler
	if len(handlers) == 1 {
		handler = handlers[0]
	} else {
		handler = newMultiWriterHandler(handlers...)
	}

	return &moduleLogger{
		module:   name,
		logger:   slog.New(handler),
		level:    moduleLevel,
		timezone: cl.timezone,
		fields:   make([]Field, 0),
	}
}

// getModuleLevelLocked returns the log level for a module (must hold read lock)
func (cl *CentralLogger) getModuleLevelLocked(module string) slog.Level {
	if level, ok := cl.moduleLevels[module]; ok {
		return level
	}
	return parseLogLevel(cl.config.DefaultLevel)
}

// Close closes all open log files
func (cl *CentralLogger) Close() error {
	if cl == nil {
		return nil
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	var errs []error

	// Close module files
	for module, file := range cl.moduleFiles {
		if err := file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close log file for module %s: %w", module, err))
		}
	}

	return errors.Join(errs...)
}

// Flush ensures all buffered logs are written
func (cl *CentralLogger) Flush() error {
	if cl == nil {
		return nil
	}

	cl.mu.RLock()
	defer cl.mu.RUnlock()

	var errs []error

	// Sync module files
	for module, file := range cl.moduleFiles {
		if err := file.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("failed to sync log file for module %s: %w", module, err))
		}
	}

	return errors.Join(errs...)
}

// ensureFileDirectory creates the directory for a file path if it doesn't exist
func ensureFileDirectory(filePath string) error {
	if filePath == "" {
		return nil
	}

	dir := filepath.Dir(filePath)
	if dir == "." || dir == filePath {
		// No directory in path or current directory
		return nil
	}

	// Create directory with appropriate permissions
	const dirPermissions = 0o700 // Owner read/write/execute only
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	return nil
}

// parseLogLevel converts string level to slog.Level
func parseLogLevel(level string) slog.Level {
	switch level {
	case "trace":
		return slog.Level(-8) // Trace level below Debug (-4)
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// moduleLogger implements Logger interface for a specific module
type moduleLogger struct {
	module   string
	logger   *slog.Logger
	level    slog.Level
	timezone *time.Location
	fields   []Field
}

// Module creates a sub-module logger
func (m *moduleLogger) Module(name string) Logger {
	if m == nil {
		return nil
	}

	return &moduleLogger{
		module:   m.module + "." + name,
		logger:   m.logger,
		level:    m.level,
		timezone: m.timezone,
		fields:   m.fields,
	}
}

// Trace logs a trace message (most verbose level)
func (m *moduleLogger) Trace(msg string, fields ...Field) {
	// Trace level is -8 (below Debug which is -4)
	const traceLevelValue = slog.Level(-8)
	if m == nil || m.level > traceLevelValue {
		return
	}
	m.log(traceLevelValue, msg, fields...)
}

// Debug logs a debug message
func (m *moduleLogger) Debug(msg string, fields ...Field) {
	if m == nil || m.level > slog.LevelDebug {
		return
	}
	m.log(slog.LevelDebug, msg, fields...)
}

// Info logs an info message
func (m *moduleLogger) Info(msg string, fields ...Field) {
	if m == nil || m.level > slog.LevelInfo {
		return
	}
	m.log(slog.LevelInfo, msg, fields...)
}

// Warn logs a warning message
func (m *moduleLogger) Warn(msg string, fields ...Field) {
	if m == nil || m.level > slog.LevelWarn {
		return
	}
	m.log(slog.LevelWarn, msg, fields...)
}

// Error logs an error message
func (m *moduleLogger) Error(msg string, fields ...Field) {
	if m == nil {
		return
	}
	m.log(slog.LevelError, msg, fields...)
}

// Log logs a message with explicit level
func (m *moduleLogger) Log(level LogLevel, msg string, fields ...Field) {
	if m == nil {
		return
	}
	m.log(parseSlogLevel(level), msg, fields...)
}

// With returns a new logger with accumulated fields
func (m *moduleLogger) With(fields ...Field) Logger {
	if m == nil {
		return nil
	}

	return &moduleLogger{
		module:   m.module,
		logger:   m.logger,
		level:    m.level,
		timezone: m.timezone,
		fields:   slices.Concat(m.fields, fields),
	}
}

// WithContext returns a logger with context values
func (m *moduleLogger) WithContext(ctx context.Context) Logger {
	if m == nil {
		return nil
	}

	if ctx == nil {
		return m
	}

	fields := make([]Field, 0, 1)

	// Extract trace ID from context if available
	if traceID := getTraceIDFromContext(ctx); traceID != "" {
		fields = append(fields, String("trace_id", traceID))
	}

	if len(fields) == 0 {
		return m
	}

	return m.With(fields...)
}

// Flush ensures all buffered logs are written
func (m *moduleLogger) Flush() error {
	// Module loggers don't manage file handles directly
	return nil
}

// log is the internal logging method
func (m *moduleLogger) log(level slog.Level, msg string, fields ...Field) {
	if m == nil {
		return
	}

	// Get attribute slice from pool (reduces allocations in hot path)
	attrsPtr := getAttrs()
	attrs := *attrsPtr

	// Add module
	if m.module != "" {
		attrs = append(attrs, slog.String("module", m.module))
	}

	// Add accumulated context fields
	for i := range m.fields {
		attrs = append(attrs, fieldToAttr(m.fields[i]))
	}

	// Add current fields
	for i := range fields {
		attrs = append(attrs, fieldToAttr(fields[i]))
	}

	m.logger.LogAttrs(context.Background(), level, msg, attrs...)

	// Return slice to pool
	*attrsPtr = attrs
	putAttrs(attrsPtr)
}

// fieldToAttr converts Field to slog.Attr
func fieldToAttr(f Field) slog.Attr {
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

// getTraceIDFromContext extracts trace ID from context
func getTraceIDFromContext(ctx context.Context) string {
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
