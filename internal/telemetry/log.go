package telemetry

import (
	"log/slog"
)

// logTelemetryInfo logs a message to the telemetry service logger if available,
// otherwise falls back to the provided fallback logger.
// This centralizes the serviceLogger nil check to avoid code duplication.
// If fallbackLogger is nil, the message is only logged if serviceLogger is available.
func logTelemetryInfo(fallbackLogger *slog.Logger, message string, keysAndValues ...any) {
	if serviceLogger != nil {
		serviceLogger.Info(message, keysAndValues...)
	} else if fallbackLogger != nil {
		fallbackLogger.Info(message, keysAndValues...)
	}
}

// logTelemetryDebug logs a debug message to the telemetry service logger if available,
// otherwise falls back to the provided fallback logger.
func logTelemetryDebug(fallbackLogger *slog.Logger, message string, keysAndValues ...any) {
	if serviceLogger != nil {
		serviceLogger.Debug(message, keysAndValues...)
	} else if fallbackLogger != nil {
		fallbackLogger.Debug(message, keysAndValues...)
	}
}

// logTelemetryWarn logs a warning message to the telemetry service logger if available,
// otherwise falls back to the provided fallback logger.
func logTelemetryWarn(fallbackLogger *slog.Logger, message string, keysAndValues ...any) {
	if serviceLogger != nil {
		serviceLogger.Warn(message, keysAndValues...)
	} else if fallbackLogger != nil {
		fallbackLogger.Warn(message, keysAndValues...)
	}
}

// logTelemetryError logs an error message to the telemetry service logger if available,
// otherwise falls back to the provided fallback logger.
func logTelemetryError(fallbackLogger *slog.Logger, message string, keysAndValues ...any) {
	if serviceLogger != nil {
		serviceLogger.Error(message, keysAndValues...)
	} else if fallbackLogger != nil {
		fallbackLogger.Error(message, keysAndValues...)
	}
}