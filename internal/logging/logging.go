package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Package logging provides structured logging capabilities using slog.

// global logger instances, initialized in Init()
var structuredLogger *slog.Logger
var humanReadableLogger *slog.Logger

// currentLogLevel stores the dynamic level for all loggers
var currentLogLevel = new(slog.LevelVar)
var initOnce sync.Once

const (
	LevelTrace = slog.Level(-8)
	LevelFatal = slog.Level(12)
)

// Add trace and fatal level names.
var levelNames = map[slog.Leveler]string{
	LevelTrace: "TRACE",
	LevelFatal: "FATAL",
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
		os.MkdirAll("logs", 0o755)

		// Structured logger (JSON) to file
		structuredLogFile, err := os.OpenFile("logs/app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			fmt.Printf("Failed to open structured log file: %v\n", err)
			structuredLogFile = os.Stderr // Fallback
		}
		structuredHandler := slog.NewJSONHandler(structuredLogFile, &slog.HandlerOptions{
			Level: currentLogLevel,
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

		// Human-readable logger (Text) to console
		humanReadableHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: currentLogLevel,
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

		// Set the default logger
		slog.SetDefault(structuredLogger)
	})
}

// SetLevel changes the logging level for all initialized loggers.
func SetLevel(level slog.Level) {
	// Update the shared level variable
	currentLogLevel.Set(level)
}

// SetOutput allows redirecting logger output, e.g., to a file.
// Note: This replaces the *entire* handler configuration.
// It now correctly preserves the globally set log level using LevelVar.
func SetOutput(structuredOutput, humanReadableOutput io.Writer) {
	// Re-initialize with new writers, using the stored LevelVar
	structuredHandler := slog.NewJSONHandler(structuredOutput, &slog.HandlerOptions{
		Level: currentLogLevel,
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
		Level: currentLogLevel,
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
