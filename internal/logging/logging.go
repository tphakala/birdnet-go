package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

var structuredLogger *slog.Logger
var humanReadableLogger *slog.Logger

const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

// Add trace and fatal level names.
var levelNames = map[slog.Leveler]string{
	LevelTrace: "TRACE",
	LevelFatal: "FATAL",
}

// Init initializes the logging system with structured and human-readable loggers.
// It configures JSON output for structured logs and Text output for human-readable logs.
func Init() {
	// Configure structured logger (JSON to stdout)
	structuredHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug, // Default level, can be configured later
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize level names
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					// Use default level name + TRACE/FATAL if not found
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
		// AddSource: true, // Uncomment if you want file/line numbers in structured logs
	})
	structuredLogger = slog.New(structuredHandler)

	// Configure human-readable logger (Text to stderr)
	humanReadableHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo, // Default level, can be configured later
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize level names for human-readable output as well
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
		// AddSource: true, // Uncomment if you want file/line numbers in human-readable logs
	})
	humanReadableLogger = slog.New(humanReadableHandler)

	// Set the default loggers
	slog.SetDefault(structuredLogger) // Set structured logger as the application default initially
}

// SetLevel sets the minimum logging level for both structured and human-readable loggers.
func SetLevel(level slog.Level) {
	// Re-initialize with the new level. A more sophisticated approach might involve
	// custom handlers that allow dynamic level changes, but this is simpler.
	// NOTE: This re-initialization might not be ideal in a concurrent environment
	// if loggers are being used while the level is changed. Consider using atomic
	// level variables or mutexes if dynamic level setting is critical during runtime.

	// Structured
	structuredHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
	})
	structuredLogger = slog.New(structuredHandler)

	// Human-readable
	humanReadableHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
	})
	humanReadableLogger = slog.New(humanReadableHandler)

	// Reset the default logger if necessary (optional)
	slog.SetDefault(structuredLogger)
}

// SetOutput allows redirecting logger output, e.g., to a file.
// Note: This replaces the *entire* handler configuration. Consider more granular controls if needed.
func SetOutput(structuredOutput, humanReadableOutput io.Writer) {
	// Get the current level from the existing handlers if possible
	var currentStructuredLevel slog.Level = slog.LevelDebug // Default
	if structuredLogger != nil {
		if leveler, ok := structuredLogger.Handler().(interface{ Level() slog.Level }); ok {
			currentStructuredLevel = leveler.Level()
		}
	}
	var currentHumanReadableLevel slog.Level = slog.LevelInfo // Default
	if humanReadableLogger != nil {
		if leveler, ok := humanReadableLogger.Handler().(interface{ Level() slog.Level }); ok {
			currentHumanReadableLevel = leveler.Level()
		}
	}

	// Re-initialize with new writers
	structuredHandler := slog.NewJSONHandler(structuredOutput, &slog.HandlerOptions{
		Level: currentStructuredLevel, // Preserve level
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
	})
	structuredLogger = slog.New(structuredHandler)

	humanReadableHandler := slog.NewTextHandler(humanReadableOutput, &slog.HandlerOptions{
		Level: currentHumanReadableLevel, // Preserve level
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
	})
	humanReadableLogger = slog.New(humanReadableHandler)

	// Set the default logger again, in case it was the one being reconfigured
	slog.SetDefault(structuredLogger)
}

// Structured returns the globally configured structured (JSON) logger.
// Returns nil if Init() has not been called.
func Structured() *slog.Logger {
	return structuredLogger
}

// HumanReadable returns the globally configured human-readable (Text) logger.
// Returns nil if Init() has not been called.
func HumanReadable() *slog.Logger {
	return humanReadableLogger
}

// ForService creates a new logger instance with the 'service' attribute added.
// It uses the global structured logger as the base.
// Returns nil if Init() has not been called.
func ForService(serviceName string) *slog.Logger {
	if structuredLogger == nil {
		return nil
	}
	return structuredLogger.With("service", serviceName)
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
func NewFileLogger(filePath, serviceName string, level slog.Level) (*slog.Logger, func() error, error) {
	// Ensure the directory exists (lumberjack doesn't create directories)
	logDir := filepath.Dir(filePath)
	if logDir != "." { // Avoid trying to create the current directory if filePath is just a filename
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("failed to create log directory %s: %w", logDir, err)
		}
	}

	// Fetch main log configuration for rotation settings
	// Using Main.Log settings as the default for all file loggers created via this func
	mainLogConf := conf.Setting().Main.Log

	// Configure lumberjack based on config
	logWriter := &lumberjack.Logger{
		Filename: filePath,
		Compress: false, // Compression can be added to LogConfig if needed later
	}

	// Default values, overridden by config below
	maxSizeMB := 100
	maxBackups := 3
	maxAge := 28 // days

	// Apply rotation settings from config
	configMaxSizeMB := int(mainLogConf.MaxSize / (1024 * 1024))
	if configMaxSizeMB > 0 {
		maxSizeMB = configMaxSizeMB
	}

	switch mainLogConf.Rotation {
	case conf.RotationDaily:
		maxAge = 1
		maxBackups = 30 // Keep up to 30 daily log files
	case conf.RotationWeekly:
		maxAge = 7
		maxBackups = 4 // Keep up to 4 weekly log files
	case conf.RotationSize:
		// Use maxSizeMB derived from config (or default if config value was invalid)
		// Use default maxAge and maxBackups unless overridden by future config options
	default:
		// Unknown rotation type, use defaults for size-based rotation
		slog.Warn("Unknown log rotation type in config, using size-based defaults", "configuredType", mainLogConf.Rotation)
	}

	logWriter.MaxSize = maxSizeMB
	logWriter.MaxBackups = maxBackups
	logWriter.MaxAge = maxAge

	// Create a handler writing to the lumberjack writer
	fileHandler := slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Customize level names
			if a.Key == slog.LevelKey {
				level := a.Value.Any().(slog.Level)
				levelLabel, exists := levelNames[level]
				if !exists {
					levelLabel = level.String()
				}
				a.Value = slog.StringValue(levelLabel)
			}
			return a
		},
		// AddSource: true, // Optional: Uncomment to include source file/line
	})

	// Create the logger and add the service attribute
	logger := slog.New(fileHandler).With("service", serviceName)

	// Return the logger and the lumberjack closer function
	// Note: lumberjack.Logger.Close() doesn't actually close the file handle
	// immediately in the typical sense, it's more for resource cleanup related
	// to its internal state if needed. The actual file handle management
	// happens internally based on rotation.
	closeFunc := func() error {
		return logWriter.Close() // Call lumberjack's Close method
	}

	return logger, closeFunc, nil
}
