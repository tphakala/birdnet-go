// exported.go
package logger

import (
	"fmt"
	"os"
)

var defaultLogger *Logger

// SetupDefaultLogger initializes the default logger with given outputs and prefix setting.
func SetupDefaultLogger(outputs map[string]LogOutput, prefix bool) {
	defaultLogger = NewLogger(outputs, prefix)
}

// Info logs an informational message using the default logger.
func Info(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Info(channel, format, a...)
	} else {
		// Handle the case where the default logger is not initialized
		fmt.Fprintf(os.Stderr, "Default logger not initialized. Unable to log INFO message: %s\n", fmt.Sprintf(format, a...))
	}
}

// Warn logs a warning message using the default logger.
func Warn(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Warning(channel, format, a...)
	} else {
		// Handle the case where the default logger is not initialized
		fmt.Fprintf(os.Stderr, "Default logger not initialized. Unable to log WARNING message: %s\n", fmt.Sprintf(format, a...))
	}
}

// Error logs an error message using the default logger.
func Error(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Error(channel, format, a...)
	} else {
		// Handle the case where the default logger is not initialized
		fmt.Fprintf(os.Stderr, "Default logger not initialized. Unable to log ERROR message: %s\n", fmt.Sprintf(format, a...))
	}
}

// Debug logs a debug message using the default logger.
func Debug(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Debug(channel, format, a...)
	} else {
		// Handle the case where the default logger is not initialized
		fmt.Fprintf(os.Stderr, "Default logger not initialized. Unable to log DEBUG message: %s\n", fmt.Sprintf(format, a...))
	}
}
