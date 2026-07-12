package logger

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	// Embed timezone database for cross-platform compatibility.
	// On Windows, the IANA timezone database may not be available,
	// causing time.LoadLocation() to fail. This import embeds the
	// timezone data directly in the binary (~450KB), ensuring
	// timezone operations work consistently on Linux, macOS, and Windows.
	_ "time/tzdata"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Constants for logger configuration
const (
	// defaultAttrCapacity is the default capacity for pooled attribute slices (module + ~7 fields)
	defaultAttrCapacity = 8

	// traceLevelValue is slog.Level for TRACE level (below Debug which is -4)
	traceLevelValue = slog.Level(-8)

	// floatPrecisionRatio rounds floats to 3 decimal places in log output
	floatPrecisionRatio = 1000.0

	// maxLevelWidth is the maximum width of log level strings for text formatting
	maxLevelWidth = 5
)

// Global logger instance
var (
	globalLogger   *CentralLogger
	globalLoggerMu sync.Mutex
)

// SetGlobal sets the global CentralLogger instance.
// This should be called once during application startup after loading configuration.
func SetGlobal(cl *CentralLogger) {
	globalLoggerMu.Lock()
	defer globalLoggerMu.Unlock()
	globalLogger = cl
}

// Global returns the global CentralLogger instance.
// If no logger has been set via SetGlobal, it returns a fallback console logger.
func Global() *CentralLogger {
	globalLoggerMu.Lock()
	defer globalLoggerMu.Unlock()

	if globalLogger != nil {
		return globalLogger
	}

	// Create a minimal console-only logger as fallback
	globalLogger = &CentralLogger{
		config: &LoggingConfig{
			DefaultLevel: DefaultLogLevel,
			Timezone:     "Local",
			Console: &ConsoleOutput{
				Enabled: true,
				Level:   DefaultLogLevel,
			},
		},
		timezone:      time.Local,
		moduleWriters: make(map[string]*BufferedFileWriter),
		moduleLevels:  make(map[string]slog.Level),
	}
	// Create console-only base handler
	globalLogger.baseHandler = newTextHandler(os.Stdout, slog.LevelInfo, time.Local)

	return globalLogger
}

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
	config        *LoggingConfig
	timezone      *time.Location
	baseHandler   slog.Handler                   // Default handler for modules without specific config
	mainWriter    *BufferedFileWriter            // Main log file writer (if file output enabled)
	moduleWriters map[string]*BufferedFileWriter // Per-module buffered writers
	moduleLevels  map[string]slog.Level          // Per-module log levels
	extraHandlers []slog.Handler                 // Additional handlers injected at construction
	mu            sync.RWMutex                   // Protects concurrent access
}

// NewCentralLogger creates a centralized logger with module routing.
// Optional extraHandlers are appended to the base handler chain so they
// receive every log record (e.g. a health-check error buffer handler).
func NewCentralLogger(cfg *LoggingConfig, extraHandlers ...slog.Handler) (*CentralLogger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("logging config cannot be nil")
	}

	// Apply defaults for nil output configurations
	// This ensures backwards compatibility when users upgrade - their existing configs
	// without explicit file_output or console sections will get sensible defaults
	// rather than silently disabling logging.
	applyConfigDefaults(cfg)

	// Load timezone
	// Special case: "Local" uses the system's local timezone
	var tz *time.Location
	switch cfg.Timezone {
	case "", "Local":
		tz = time.Local
	default:
		var err error
		tz, err = time.LoadLocation(cfg.Timezone)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone %s: %w", cfg.Timezone, err)
		}
	}

	cl := &CentralLogger{
		config:        cfg,
		timezone:      tz,
		moduleWriters: make(map[string]*BufferedFileWriter),
		moduleLevels:  make(map[string]slog.Level),
		extraHandlers: extraHandlers,
	}

	// Parse module levels
	for module, levelStr := range cfg.ModuleLevels {
		cl.moduleLevels[module] = parseLogLevel(levelStr)
	}

	// Create base handler (console and/or main file)
	if err := cl.createBaseHandler(); err != nil {
		return nil, fmt.Errorf("failed to create base handler: %w", err)
	}

	// Open module-specific log files with buffered writers
	// On error, clean up already-opened writers to prevent resource leaks
	for module, moduleConfig := range cfg.ModuleOutputs {
		if !moduleConfig.Enabled {
			continue
		}

		// Ensure directory exists
		if err := ensureFileDirectory(moduleConfig.FilePath); err != nil {
			cl.closeAllWriters() // Clean up on error
			return nil, fmt.Errorf("failed to create directory for module %s: %w", module, err)
		}

		// Build writer options including rotation if configured
		// Module config falls back to FileOutput defaults for rotation settings
		var writerOpts []BufferedWriterOption
		rotationConfig := RotationConfigFromModuleOutput(&moduleConfig, cfg.FileOutput)
		if rotationConfig.IsEnabled() {
			writerOpts = append(writerOpts, WithRotation(rotationConfig))
		}

		// Create buffered writer for module log file
		writer, err := NewBufferedFileWriter(moduleConfig.FilePath, writerOpts...)
		if err != nil {
			cl.closeAllWriters() // Clean up on error
			return nil, fmt.Errorf("failed to create log writer for module %s: %w", module, err)
		}
		cl.moduleWriters[module] = writer
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

		// Build writer options including rotation if configured
		var writerOpts []BufferedWriterOption
		rotationConfig := RotationConfigFromFileOutput(cl.config.FileOutput)
		if rotationConfig.IsEnabled() {
			writerOpts = append(writerOpts, WithRotation(rotationConfig))
		}

		// Create buffered writer for main log file
		writer, err := NewBufferedFileWriter(cl.config.FileOutput.Path, writerOpts...)
		if err != nil {
			return fmt.Errorf("failed to create log writer: %w", err)
		}

		// Store the writer for proper cleanup
		cl.mainWriter = writer

		fileLevel := parseLogLevel(cl.config.FileOutput.Level)
		opts := &slog.HandlerOptions{
			Level: fileLevel,
			ReplaceAttr: timeZoneReplaceAttr(cl.timezone),
		}
		handlers = append(handlers, slog.NewJSONHandler(writer, opts))
	}

	if len(handlers) == 0 {
		// Fallback to stdout text handler if nothing is configured
		handlers = append(handlers, newTextHandler(os.Stdout, parseLogLevel(cl.config.DefaultLevel), cl.timezone))
	}

	// Append any extra handlers injected at construction (e.g. health error buffer)
	for _, h := range cl.extraHandlers {
		if h != nil {
			handlers = append(handlers, h)
		}
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
		if moduleWriter, ok := cl.moduleWriters[name]; ok {
			opts := &slog.HandlerOptions{
				Level: moduleLevel,
			}
			handlers = append(handlers, slog.NewJSONHandler(moduleWriter, opts))
		}

		// Also log to console if requested - USE TEXT FORMAT
		// Use the console's configured level (not the module's) so that
		// modules with debug-level file output don't flood stdout.
		if moduleConfig.ConsoleAlso && cl.config.Console != nil && cl.config.Console.Enabled {
			consoleLevel := parseLogLevel(cl.config.Console.Level)
			handlers = append(handlers, newTextHandler(os.Stdout, consoleLevel, cl.timezone))
		}

		// Include extra handlers (e.g. health error buffer) so modules with
		// dedicated file output still feed into the shared diagnostics buffer.
		for _, h := range cl.extraHandlers {
			if h != nil {
				handlers = append(handlers, h)
			}
		}
	} else {
		// Use base handler (console + main file + extra handlers)
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
		fields:   nil, // nil is equivalent to empty slice but avoids allocation
	}
}

// getModuleLevelLocked returns the log level for a module, walking up the
// hierarchy from most-specific to least-specific. For "analysis.processor",
// it checks "analysis.processor" first, then "analysis", then falls back to
// DefaultLevel. At each level, both moduleLevels and ModuleOutputs are checked
// so that an exact child match in either source wins over a parent match.
func (cl *CentralLogger) getModuleLevelLocked(module string) slog.Level {
	for name := module; name != ""; {
		if level, ok := cl.moduleLevels[name]; ok {
			return level
		}
		if modOut, ok := cl.config.ModuleOutputs[name]; ok && modOut.Level != "" {
			return parseLogLevel(modOut.Level)
		}
		if idx := strings.LastIndex(name, "."); idx >= 0 {
			name = name[:idx]
		} else {
			break
		}
	}
	return parseLogLevel(cl.config.DefaultLevel)
}

// GetOutputPath returns the file path configured for a given module.
// Returns empty string if the module writes only to console.
func (cl *CentralLogger) GetOutputPath(module string) string {
	if cl == nil {
		return ""
	}

	cl.mu.RLock()
	defer cl.mu.RUnlock()

	if modOut, ok := cl.config.ModuleOutputs[module]; ok && modOut.Enabled {
		return modOut.FilePath
	}

	return ""
}

// GetDefaultOutputPath returns the default file output path (application.log).
// Returns empty string if file output is not configured.
func (cl *CentralLogger) GetDefaultOutputPath() string {
	if cl == nil {
		return ""
	}

	cl.mu.RLock()
	defer cl.mu.RUnlock()

	if cl.config.FileOutput != nil && cl.config.FileOutput.Enabled {
		return cl.config.FileOutput.Path
	}

	return ""
}

// Close closes all buffered writers and their underlying files
func (cl *CentralLogger) Close() error {
	if cl == nil {
		return nil
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	return cl.closeAllWritersLocked()
}

// closeAllWriters closes all writers without locking (for use during initialization errors)
func (cl *CentralLogger) closeAllWriters() {
	_ = cl.closeAllWritersLocked()
}

// closeAllWritersLocked closes all writers (caller must hold lock or be in init)
func (cl *CentralLogger) closeAllWritersLocked() error {
	var errs []error

	// Close main log writer (flushes buffer and syncs to disk)
	if cl.mainWriter != nil {
		if err := cl.mainWriter.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close main log writer: %w", err))
		}
		cl.mainWriter = nil
	}

	// Close module writers
	for module, writer := range cl.moduleWriters {
		if err := writer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close log writer for module %s: %w", module, err))
		}
	}
	// Clear the map to avoid referencing closed writers
	cl.moduleWriters = nil

	return errors.Join(errs...)
}

// Flush ensures all buffered logs are written to OS buffers.
// Note: This flushes to OS buffers but does not fsync to disk.
// For critical sync to disk, Close() will perform full sync.
func (cl *CentralLogger) Flush() error {
	if cl == nil {
		return nil
	}

	cl.mu.RLock()
	defer cl.mu.RUnlock()

	var errs []error

	// Flush main log writer
	if cl.mainWriter != nil {
		if err := cl.mainWriter.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("failed to flush main log writer: %w", err))
		}
	}

	// Flush module writers
	for module, writer := range cl.moduleWriters {
		if err := writer.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("failed to flush log writer for module %s: %w", module, err))
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
		return traceLevelValue
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

// isValid reports whether the moduleLogger is properly initialized with a
// non-nil inner slog.Logger.  It is safe to call on a nil receiver.
func (m *moduleLogger) isValid() bool {
	return m != nil && m.logger != nil
}

// Module creates a sub-module logger.
// The returned logger shares the parent's slog.Logger but gets its own copy of fields
// to ensure immutability - modifications to parent fields won't affect children.
func (m *moduleLogger) Module(name string) Logger {
	if m == nil {
		return nil
	}
	if !m.isValid() {
		return m
	}

	return &moduleLogger{
		module:   m.module + "." + name,
		logger:   m.logger,
		level:    m.level,
		timezone: m.timezone,
		fields:   slices.Clone(m.fields), // clone to ensure immutability
	}
}

// Trace logs a trace message (most verbose level)
func (m *moduleLogger) Trace(msg string, fields ...Field) {
	if !m.isValid() || m.level > traceLevelValue {
		return
	}
	m.log(traceLevelValue, msg, fields...)
}

// Debug logs a debug message
func (m *moduleLogger) Debug(msg string, fields ...Field) {
	if !m.isValid() || m.level > slog.LevelDebug {
		return
	}
	m.log(slog.LevelDebug, msg, fields...)
}

// Info logs an info message
func (m *moduleLogger) Info(msg string, fields ...Field) {
	if !m.isValid() || m.level > slog.LevelInfo {
		return
	}
	m.log(slog.LevelInfo, msg, fields...)
}

// Warn logs a warning message
func (m *moduleLogger) Warn(msg string, fields ...Field) {
	if !m.isValid() || m.level > slog.LevelWarn {
		return
	}
	m.log(slog.LevelWarn, msg, fields...)
}

// Error logs an error message
func (m *moduleLogger) Error(msg string, fields ...Field) {
	if !m.isValid() {
		return
	}
	m.log(slog.LevelError, msg, fields...)
}

// Log logs a message with explicit level
func (m *moduleLogger) Log(level LogLevel, msg string, fields ...Field) {
	if !m.isValid() {
		return
	}
	m.log(parseSlogLevel(level), msg, fields...)
}

// With returns a new logger with accumulated fields
func (m *moduleLogger) With(fields ...Field) Logger {
	if m == nil {
		return nil
	}
	if !m.isValid() {
		return m
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
	if !m.isValid() {
		return m
	}

	if ctx == nil {
		return m
	}

	// Extract trace ID from context if available
	// Check first to avoid allocation when no trace ID exists
	traceID := getTraceIDFromContext(ctx)
	if traceID == "" {
		return m
	}

	return m.With(String(traceIDKey, traceID))
}

// Flush ensures all buffered logs are written
func (m *moduleLogger) Flush() error {
	// Module loggers don't manage file handles directly
	return nil
}

// log is the internal logging method
func (m *moduleLogger) log(level slog.Level, msg string, fields ...Field) {
	if !m.isValid() {
		return
	}

	// Get attribute slice from pool (reduces allocations in hot path)
	attrsPtr := getAttrs()
	attrs := *attrsPtr

	// Add module
	if m.module != "" {
		attrs = append(attrs, slog.String(moduleKey, m.module))
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

// roundFloat rounds a float64 to 3 decimal places.
// Used to produce cleaner log output (e.g., 1.234 instead of 1.23456789).
// Uses pre-computed floatPrecisionRatio to avoid math.Pow per call.
func roundFloat(val float64) float64 {
	return math.Round(val*floatPrecisionRatio) / floatPrecisionRatio
}

// floatAttr renders a float value as a slog attribute. slog's JSON handler
// cannot encode a non-finite float (+Inf, -Inf, NaN); it substitutes an
// "!ERROR:json: unsupported value" string that corrupts the whole log line. A
// non-finite value is therefore rendered as its symbolic string form (matching
// Go's %v: "+Inf"/"-Inf"/"NaN"), while a finite value keeps the rounded numeric
// form. The non-finite branch is cold, so the common path stays allocation-free.
func floatAttr(key string, v float64) slog.Attr {
	switch {
	case math.IsNaN(v):
		return slog.String(key, "NaN")
	case math.IsInf(v, 1):
		return slog.String(key, "+Inf")
	case math.IsInf(v, -1):
		return slog.String(key, "-Inf")
	default:
		// roundFloat multiplies by floatPrecisionRatio, which can overflow a very
		// large finite magnitude to ±Inf; fall back to the raw (still finite)
		// value in that rare case so it stays an encodable JSON number.
		if r := roundFloat(v); !math.IsInf(r, 0) {
			return slog.Float64(key, r)
		}
		return slog.Float64(key, v)
	}
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
	case float32:
		// Round for cleaner output; guard non-finite values (see floatAttr).
		return floatAttr(f.Key, float64(v))
	case float64:
		// Round for cleaner output; guard non-finite values (see floatAttr).
		return floatAttr(f.Key, v)
	case bool:
		return slog.Bool(f.Key, v)
	case time.Time:
		return slog.Time(f.Key, v)
	case time.Duration:
		// Format as human-readable string (e.g., "5ms", "1.5s") for consistent output
		// slog.Duration outputs nanoseconds in JSON which is not human-friendly
		return slog.String(f.Key, v.Round(time.Millisecond).String())
	default:
		return slog.Any(f.Key, sanitizeAny(v))
	}
}

// sanitizeAny recursively sanitizes a value passed to logger.Any to ensure it contains
// no non-finite floats (NaN, +Inf, -Inf) which would corrupt the JSON log output.
func sanitizeAny(v any) any {
	if v == nil {
		return nil
	}

	// Fast path for common basic types to avoid reflection overhead
	switch val := v.(type) {
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return logSafeFloat(val)
		}
		return val
	case float32:
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			return logSafeFloat(float64(val))
		}
		return val
	case int, int64, string, bool:
		return val
	}

	rv := reflect.ValueOf(v)
	if san, mod := sanitizeReflect(rv, make(map[uintptr]bool)); mod {
		return san
	}
	return v
}

func logSafeFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "+inf"
	case math.IsInf(f, -1):
		return "-inf"
	default:
		// Should not be reached, but fallback safely
		return "unknown"
	}
}

// sanitizeReflect recursively traverses the reflect.Value and returns a sanitized any.
// It tracks visited pointers to avoid stack overflows on cyclic data.
// If no non-finite floats are found, it returns (nil, false) to avoid heap allocations.
//nolint:gocognit,cyclop,gocyclo // Recursive reflection is inherently complex
func sanitizeReflect(rv reflect.Value, visited map[uintptr]bool) (any, bool) {
	if !rv.IsValid() || !rv.CanInterface() {
		return nil, false
	}

	// Trust custom serialization for types that implement it natively.
	// encoding/json relies on json.Marshaler, slog relies on slog.LogValuer
	iface := rv.Interface()
	if _, ok := iface.(slog.LogValuer); ok {
		return nil, false
	}
	if _, ok := iface.(fmt.Stringer); ok {
		return nil, false
	}
	if _, ok := iface.(error); ok {
		return nil, false
	}

	// Avoid descending into json.Marshaler (we do this via reflection to avoid
	// unnecessarily importing encoding/json here if not needed, though the package might).
	if rv.Type().Implements(reflect.TypeFor[interface{ MarshalJSON() ([]byte, error) }]()) {
		return nil, false
	}

	// Also check if the pointer to the value implements these interfaces (if addressable)
	if rv.CanAddr() {
		addr := rv.Addr()
		ifaceAddr := addr.Interface()
		if _, ok := ifaceAddr.(slog.LogValuer); ok {
			return nil, false
		}
		if _, ok := ifaceAddr.(fmt.Stringer); ok {
			return nil, false
		}
		if _, ok := ifaceAddr.(error); ok {
			return nil, false
		}
		if addr.Type().Implements(reflect.TypeFor[interface{ MarshalJSON() ([]byte, error) }]()) {
			return nil, false
		}
	}

	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return logSafeFloat(f), true
		}
		return nil, false

	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 || (rv.Kind() == reflect.Slice && rv.IsNil()) {
			return nil, false
		}
		if rv.Kind() == reflect.Slice {
			ptr := rv.Pointer()
			if visited[ptr] {
				return "[Circular]", true
			}
			visited[ptr] = true
			defer delete(visited, ptr)
		}

		needsCopy := false
		for i := range rv.Len() {
			if _, mod := sanitizeReflect(rv.Index(i), visited); mod {
				needsCopy = true
				break
			}
		}
		if !needsCopy {
			return nil, false
		}

		cp := make([]any, rv.Len())
		for i := range rv.Len() {
			elem := rv.Index(i)
			san, mod := sanitizeReflect(elem, visited)
			if mod {
				cp[i] = san
			} else if elem.CanInterface() {
				cp[i] = elem.Interface()
			}
		}
		return cp, true

	case reflect.Map:
		if rv.Len() == 0 || rv.IsNil() {
			return nil, false
		}
		ptr := rv.Pointer()
		if visited[ptr] {
			return "[Circular]", true
		}
		visited[ptr] = true
		defer delete(visited, ptr)

		needsCopy := false
		iter := rv.MapRange()
		for iter.Next() {
			_, kMod := sanitizeReflect(iter.Key(), visited)
			_, vMod := sanitizeReflect(iter.Value(), visited)
			if kMod || vMod {
				needsCopy = true
				break
			}
		}
		if !needsCopy {
			return nil, false
		}

		cp := make(map[string]any, rv.Len())
		iter = rv.MapRange()
		for iter.Next() {
			k := iter.Key()
			v := iter.Value()
			kSan, kMod := sanitizeReflect(k, visited)
			vSan, vMod := sanitizeReflect(v, visited)

			kStr := ""
			switch {
			case k.Kind() == reflect.String:
				kStr = k.String()
			case kMod:
				kStr = fmt.Sprint(kSan)
			case k.CanInterface():
				kStr = fmt.Sprint(k.Interface())
			}

			// Prevent key collisions for distinct keys that stringify to the same value
			origKStr := kStr
			counter := 1
			for {
				if _, exists := cp[kStr]; !exists {
					break
				}
				kStr = fmt.Sprintf("%s_%d", origKStr, counter)
				counter++
			}

			switch {
			case vMod:
				cp[kStr] = vSan
			case v.CanInterface():
				cp[kStr] = v.Interface()
			}
		}
		return cp, true

	case reflect.Struct:
		if !structNeedsCopy(rv, visited) {
			return nil, false
		}
		cp := make(map[string]any)
		flattenStruct(rv, cp, visited)
		return cp, true

	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil, false
		}
		if rv.Kind() == reflect.Pointer {
			ptr := rv.Pointer()
			if visited[ptr] {
				return "[Circular]", true
			}
			visited[ptr] = true
			defer delete(visited, ptr)
		}

		san, mod := sanitizeReflect(rv.Elem(), visited)
		if mod {
			return san, true
		}
		return nil, false

	default:
		return nil, false
	}
}

// getTraceIDFromContext extracts trace ID from context.
// Use WithTraceID() to set trace IDs in context.
func getTraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		return traceID
	}
	return ""
}

//nolint:gocognit // Recursive struct flattening naturally has higher complexity
func structNeedsCopy(rv reflect.Value, visited map[uintptr]bool) bool {
	if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	//nolint:gocritic // rv.NumField() is appropriate for iterating over fields
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		fv := rv.Field(i)
		
		if field.Anonymous {
			fvElem := fv
			var ptr uintptr
			var isPtr bool
			if fvElem.Kind() == reflect.Pointer || fvElem.Kind() == reflect.Interface {
				if !fvElem.IsNil() {
					if fvElem.Kind() == reflect.Pointer {
						ptr = fvElem.Pointer()
						isPtr = true
					}
					fvElem = fvElem.Elem()
				}
			}
			if fvElem.Kind() == reflect.Struct {
				if isPtr {
					if visited[ptr] {
						continue
					}
					visited[ptr] = true
				}
				if structNeedsCopy(fvElem, visited) {
					if isPtr {
						delete(visited, ptr)
					}
					return true
				}
				if isPtr {
					delete(visited, ptr)
				}
				continue
			}
		}

		if _, mod := sanitizeReflect(fv, visited); mod {
			return true
		}
	}
	return false
}

//nolint:gocognit // Recursive struct flattening naturally has higher complexity
func flattenStruct(rv reflect.Value, cp map[string]any, visited map[uintptr]bool) {
	if rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return
	}
	//nolint:gocritic // rv.NumField() is appropriate for iterating over fields in a struct.
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		fv := rv.Field(i)
		if field.Anonymous {
			fvElem := fv
			var ptr uintptr
			var isPtr bool
			if fvElem.Kind() == reflect.Pointer || fvElem.Kind() == reflect.Interface {
				if !fvElem.IsNil() {
					if fvElem.Kind() == reflect.Pointer {
						ptr = fvElem.Pointer()
						isPtr = true
					}
					fvElem = fvElem.Elem()
				}
			}
			if fvElem.Kind() == reflect.Struct {
				if isPtr {
					if visited[ptr] {
						continue
					}
					visited[ptr] = true
				}
				flattenStruct(fvElem, cp, visited)
				if isPtr {
					delete(visited, ptr)
				}
				continue
			}
		}

		key := field.Name
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}

		omitempty := false
		omitzero := false
		if tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] != "" {
				key = parts[0]
			}
			for _, part := range parts[1:] {
				switch part {
				case "omitempty":
					omitempty = true
				case "omitzero":
					omitzero = true
				}
			}
		}

		san, mod := sanitizeReflect(fv, visited)
		var val any
		if mod {
			val = san
		} else if fv.CanInterface() {
			val = fv.Interface()
		}

		if omitempty && isEmptyValue(fv, val) {
			continue
		}
		if omitzero && isZeroValue(fv) {
			continue
		}
		cp[key] = val
	}
}

func isEmptyValue(v reflect.Value, val any) bool {
	if val == nil {
		return true
	}
	//nolint:exhaustive // Intentionally handling only types that can be empty values
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	}
	return false
}

func isZeroValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	return v.IsZero()
}

func timeZoneReplaceAttr(tz *time.Location) func(groups []string, a slog.Attr) slog.Attr {
	if tz == nil || tz == time.UTC {
		return nil
	}
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey && len(groups) == 0 {
			if t, ok := a.Value.Any().(time.Time); ok {
				return slog.Time(a.Key, t.In(tz))
			}
		}
		return a
	}
}
