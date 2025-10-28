package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Package logging provides structured logging capabilities using slog.

// global logger instances, initialized in Init()
var (
	structuredLogger    *slog.Logger
	humanReadableLogger *slog.Logger
	loggerMu            sync.RWMutex // Protects logger access
)

// Track closable writers for proper resource management in SetOutput
var currentStructuredOutputCloser io.Closer
var currentHumanReadableOutputCloser io.Closer

// currentLogLevel stores the dynamic level for all loggers
var currentLogLevel = new(slog.LevelVar)
var initOnce sync.Once
var initialized bool

const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

// Add trace and fatal level names.
var levelNames = map[slog.Leveler]string{
	LevelTrace: "TRACE",
	LevelFatal: "FATAL",
}

// defaultReplaceAttr provides common attribute formatting for all loggers.
// It formats time, customizes level names, and truncates floats to 2 decimal places.
func defaultReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	// Format time to second precision (RFC3339 without sub-seconds)
	if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02T15:04:05Z07:00"))
	}
	// Customize level names
	if a.Key == slog.LevelKey {
		// Safety check: ensure the value is actually a slog.Level
		if level, ok := a.Value.Any().(slog.Level); ok {
			levelLabel, exists := levelNames[level]
			if !exists {
				levelLabel = level.String()
			}
			a.Value = slog.StringValue(levelLabel)
		} else {
			// If it's not a slog.Level, convert it to string to avoid panic
			// This can happen when user code accidentally uses "level" as an attribute key
			a.Value = slog.StringValue(fmt.Sprintf("%v", a.Value.Any()))
		}
	}
	// Truncate float64 values to 2 decimal places
	if a.Value.Kind() == slog.KindFloat64 {
		// Multiply by 100, truncate the decimal part, then divide by 100.0
		truncatedVal := math.Trunc(a.Value.Float64()*100) / 100.0
		a.Value = slog.Float64Value(truncatedVal)
	}
	return a
}

// Init initializes the global loggers based on configuration.
// It sets up both a structured (JSON) logger and a human-readable (Text) logger.
func Init() {
	initOnce.Do(func() {
		// Set the initial level (defaulting to Info)
		// TODO: Determine if a global config setting should drive this initial level.
		// For now, we rely on the default LevelInfo or explicit SetLevel calls.
		currentLogLevel.Set(slog.LevelInfo)

		// Ensure logs directory exists
		err := os.MkdirAll("logs", 0o755) //nolint:gosec // accept 0o755 for now
		if err != nil {
			fmt.Printf("Failed to create logs directory: %v\n", err)
			os.Exit(1) // bail out if we can't create the logs directory
		}

		// Structured logger (JSON) to file
		structuredLogFile, err := os.OpenFile("logs/app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666) //nolint:gosec // accept 0o666 for now
		if err != nil {
			fmt.Printf("Failed to open structured log file: %v\n", err)
			structuredLogFile = os.Stderr // Fallback
		}
		// Store the closable file handle (only if it's not stderr)
		if structuredLogFile != os.Stderr {
			currentStructuredOutputCloser = structuredLogFile
		} else {
			currentStructuredOutputCloser = nil // Ensure it's nil if we fell back to stderr
		}

		structuredHandler := slog.NewJSONHandler(structuredLogFile, &slog.HandlerOptions{
			Level:       currentLogLevel,
			ReplaceAttr: defaultReplaceAttr,
		})

		// Human-readable logger (Text) to console
		// os.Stdout is not typically closed by the application, so no closer needed here.
		currentHumanReadableOutputCloser = nil
		humanReadableHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level:       currentLogLevel,
			ReplaceAttr: defaultReplaceAttr,
		})

		// Set loggers with lock protection
		loggerMu.Lock()
		structuredLogger = slog.New(structuredHandler)
		humanReadableLogger = slog.New(humanReadableHandler)
		loggerMu.Unlock()

		// Set the default logger
		slog.SetDefault(structuredLogger)

		// Mark as initialized
		initialized = true
	})
}

// IsInitialized returns true if the logging system has been initialized
func IsInitialized() bool {
	return initialized
}

// SetLevel changes the logging level for all initialized loggers.
func SetLevel(level slog.Level) {
	// Update the shared level variable
	currentLogLevel.Set(level)
}

// SetOutput allows redirecting logger output, e.g., to a file.
// It safely closes any previously opened closable writers before creating new ones.
// Returns an error if either provided writer is nil.
func SetOutput(structuredOutput, humanReadableOutput io.Writer) error {
	// Input validation
	if structuredOutput == nil {
		return errors.New("structuredOutput writer cannot be nil")
	}
	if humanReadableOutput == nil {
		return errors.New("humanReadableOutput writer cannot be nil")
	}

	// Close existing closable writers
	var closeErrors []error
	if currentStructuredOutputCloser != nil {
		if err := currentStructuredOutputCloser.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("failed to close previous structured output: %w", err))
		}
		currentStructuredOutputCloser = nil // Reset even if close failed
	}
	if currentHumanReadableOutputCloser != nil {
		if err := currentHumanReadableOutputCloser.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("failed to close previous human-readable output: %w", err))
		}
		currentHumanReadableOutputCloser = nil // Reset even if close failed
	}

	// Re-initialize with new writers, using the stored LevelVar
	structuredHandler := slog.NewJSONHandler(structuredOutput, &slog.HandlerOptions{
		Level:       currentLogLevel,
		ReplaceAttr: defaultReplaceAttr,
	})

	humanReadableHandler := slog.NewTextHandler(humanReadableOutput, &slog.HandlerOptions{
		Level:       currentLogLevel,
		ReplaceAttr: defaultReplaceAttr,
	})

	// Update loggers with lock protection
	loggerMu.Lock()
	structuredLogger = slog.New(structuredHandler)
	humanReadableLogger = slog.New(humanReadableHandler)
	loggerMu.Unlock()

	// Track the new closers if they implement io.Closer
	if c, ok := structuredOutput.(io.Closer); ok {
		currentStructuredOutputCloser = c
	}
	if c, ok := humanReadableOutput.(io.Closer); ok {
		currentHumanReadableOutputCloser = c
	}

	// Set the default logger again, in case it was the one being reconfigured
	slog.SetDefault(structuredLogger)

	// Return combined errors from closing previous writers, if any
	if len(closeErrors) > 0 {
		return errors.Join(closeErrors...)
	}

	return nil
}

// Structured returns the globally configured structured (JSON) logger.
// Returns nil if Init() has not been called.
func Structured() *slog.Logger {
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return structuredLogger
}

// HumanReadable returns the globally configured human-readable (Text) logger.
// Returns nil if Init() has not been called.
func HumanReadable() *slog.Logger {
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return humanReadableLogger
}

// ForService creates a new logger instance with the 'service' attribute added.
// It uses the global structured logger as the base.
// Returns nil if Init() has not been called.
func ForService(serviceName string) *slog.Logger {
	loggerMu.RLock()
	logger := structuredLogger
	loggerMu.RUnlock()

	if logger == nil {
		return nil
	}
	return logger.With("service", serviceName)
}

// --- Convenience functions using the default logger ---

// Debug logs a debug message using the default slog logger.
func Debug(msg string, args ...any) {
	slog.Debug(msg, args...)
}

// Info logs an info message using the default slog logger.
func Info(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Warn logs a warning message using the default slog logger.
func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

// Error logs an error message using the default slog logger.
func Error(msg string, args ...any) {
	slog.Error(msg, args...)
}

// Fatal logs a fatal message using the custom Fatal level and then exits.
// Uses the default logger.
func Fatal(msg string, args ...any) {
	slog.Log(context.TODO(), LevelFatal, msg, args...)
	os.Exit(1)
}

// Trace logs a trace message using the custom Trace level.
// Uses the default logger.
func Trace(msg string, args ...any) {
	slog.Log(context.TODO(), LevelTrace, msg, args...)
}

// NewFileLogger creates a new slog.Logger instance configured to write JSON logs
// to the specified file path using lumberjack for rotation based on global config.
// It includes a 'service' attribute in all logs.
// It returns the logger, a function to close the underlying log writer, and an error if setup fails.
func NewFileLogger(filePath, serviceName string, levelVar *slog.LevelVar) (*slog.Logger, func() error, error) {
	// Ensure the directory exists (lumberjack doesn't create directories)
	logDir := filepath.Dir(filePath)
	if logDir != "." { // Avoid trying to create the current directory if filePath is just a filename
		if err := os.MkdirAll(logDir, 0o755); err != nil { //nolint:gosec // accept 0o755 for now
			return nil, nil, fmt.Errorf("failed to create log directory %s: %w", logDir, err)
		}
	}

	// Configure lumberjack logger based on global config settings
	// Using Main.Log settings as the default for all file loggers created via this func
	mainLogConf := conf.Setting().Main.Log

	lj := &lumberjack.Logger{
		Filename: filePath,
		Compress: false, // Compression can be added later if needed
	}

	// Apply rotation settings from config
	// Default values
	maxSizeMB := 100
	maxBackups := 3
	maxAge := 28 // days

	configMaxSizeMB := int(mainLogConf.MaxSize / (1024 * 1024))
	if configMaxSizeMB > 0 {
		maxSizeMB = configMaxSizeMB
	}

	switch mainLogConf.Rotation {
	case conf.RotationDaily:
		maxAge = 1
		maxBackups = 30
	case conf.RotationWeekly:
		maxAge = 7
		maxBackups = 4
	case conf.RotationSize:
		// Size-based rotation uses maxSizeMB derived from config (or default)
	default:
		slog.Warn("Unknown log rotation type in config, using size-based defaults", "configuredType", mainLogConf.Rotation)
	}

	lj.MaxSize = maxSizeMB
	lj.MaxBackups = maxBackups
	lj.MaxAge = maxAge

	// Create the slog handler using the lumberjack writer
	handler := slog.NewJSONHandler(lj, &slog.HandlerOptions{
		AddSource:   false, // Keep this false unless specifically needed for debugging
		Level:       levelVar,
		ReplaceAttr: defaultReplaceAttr,
	})

	// Create the logger and add the service attribute
	logger := slog.New(handler).With("service", serviceName)

	// Return the logger and the lumberjack closer function
	// Note: lumberjack.Logger.Close() doesn't actually close the file handle
	// immediately in the typical sense, it's more for resource cleanup related
	// to its internal state if needed. The actual file handle management
	// happens internally based on rotation.
	closeFunc := func() error {
		return lj.Close() // Call lumberjack's Close method
	}

	return logger, closeFunc, nil
}
