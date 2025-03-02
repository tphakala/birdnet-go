// global.go
package logger

import (
	"fmt"
	"os"
	"sync"
)

// Package-level variables for the global logger
var (
	defaultLogger     *Logger
	defaultLoggerInit sync.Once
	errDefaultLogger  error
)

// InitGlobal initializes the default global logger with the given configuration
// It can only be called once; subsequent calls will be ignored
func InitGlobal(config Config) error {
	defaultLoggerInit.Do(func() {
		// Make a copy of the configuration to avoid modifying the original
		configCopy := config

		// Force disable caller information for the global logger
		configCopy.DisableCaller = true

		// Use the modified config
		defaultLogger, errDefaultLogger = NewLogger(configCopy)
	})
	return errDefaultLogger
}

// GetGlobal returns the default global logger instance
// If the logger has not been initialized, it will initialize a basic stdout logger
func GetGlobal() *Logger {
	defaultLoggerInit.Do(func() {
		// Default configuration logs to stdout with colored output in development mode
		config := DefaultConfig()

		// Force disable caller information for the global logger
		config.DisableCaller = true

		defaultLogger, errDefaultLogger = NewLogger(config)
		if errDefaultLogger != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize default logger: %v\n", errDefaultLogger)
			os.Exit(1)
		}
	})
	return defaultLogger
}

// Debug logs a message at debug level using the global logger
func Debug(msg string, fields ...interface{}) {
	GetGlobal().Debug(msg, fields...)
}

// Info logs a message at info level using the global logger
func Info(msg string, fields ...interface{}) {
	GetGlobal().Info(msg, fields...)
}

// Warn logs a message at warn level using the global logger
func Warn(msg string, fields ...interface{}) {
	GetGlobal().Warn(msg, fields...)
}

// Error logs a message at error level using the global logger
func Error(msg string, fields ...interface{}) {
	GetGlobal().Error(msg, fields...)
}

// Fatal logs a message at fatal level using the global logger and then exits
func Fatal(msg string, fields ...interface{}) {
	GetGlobal().Fatal(msg, fields...)
}

// Named returns a named logger derived from the global logger
func Named(name string) *Logger {
	return GetGlobal().Named(name)
}

// With returns a logger with the given fields added to the context
func With(fields ...interface{}) *Logger {
	return GetGlobal().With(fields...)
}

// Sync flushes any buffered log entries from the global logger
func Sync() error {
	return GetGlobal().Sync()
}
