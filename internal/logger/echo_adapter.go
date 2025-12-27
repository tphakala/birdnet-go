package logger

import (
	"fmt"
	"io"

	echo_log "github.com/labstack/gommon/log"
)

// EchoLoggerAdapter adapts pkg/logger.Logger to echo.Logger interface.
// This allows Echo framework to use our centralized logging system instead of
// its built-in logger, ensuring consistent log format and routing.
//
// Usage:
//
//	e := echo.New()
//	e.Logger = logger.NewEchoLoggerAdapter(appLogger.Module("echo"))
type EchoLoggerAdapter struct {
	logger Logger
}

// NewEchoLoggerAdapter creates a new Echo logger adapter using pkg/logger
func NewEchoLoggerAdapter(logger Logger) *EchoLoggerAdapter {
	if logger == nil {
		logger = NewSlogLogger(nil, LogLevelInfo, nil)
	}
	return &EchoLoggerAdapter{logger: logger}
}

// Output returns the output destination (not used, output is managed by our logger)
func (a *EchoLoggerAdapter) Output() io.Writer {
	return io.Discard
}

// SetOutput sets the output destination (no-op, output is managed by our logger)
func (a *EchoLoggerAdapter) SetOutput(_ io.Writer) {
	// No-op: output is managed by our logger
}

// Prefix returns the log prefix (not used, module scoping provides context)
func (a *EchoLoggerAdapter) Prefix() string {
	return ""
}

// SetPrefix sets the log prefix (no-op, module scoping provides context)
func (a *EchoLoggerAdapter) SetPrefix(_ string) {
	// No-op: prefix is managed by module scoping
}

// Level returns the current log level
func (a *EchoLoggerAdapter) Level() echo_log.Lvl {
	return echo_log.INFO
}

// SetLevel sets the log level (no-op, level is managed by our logger config)
func (a *EchoLoggerAdapter) SetLevel(_ echo_log.Lvl) {
	// No-op: level is managed by our logger configuration
}

// SetHeader sets the log header format (no-op, format is managed by our logger)
func (a *EchoLoggerAdapter) SetHeader(_ string) {
	// No-op: format is managed by our logger
}

// Print logs a message at INFO level
func (a *EchoLoggerAdapter) Print(i ...any) {
	a.logger.Info(fmt.Sprint(i...))
}

// Printf logs a formatted message at INFO level
func (a *EchoLoggerAdapter) Printf(format string, args ...any) {
	a.logger.Info(fmt.Sprintf(format, args...))
}

// Printj logs a JSON object at INFO level
func (a *EchoLoggerAdapter) Printj(j echo_log.JSON) {
	a.logger.Info("Echo JSON log", Any("data", j))
}

// Debug logs a message at DEBUG level
func (a *EchoLoggerAdapter) Debug(i ...any) {
	a.logger.Debug(fmt.Sprint(i...))
}

// Debugf logs a formatted message at DEBUG level
func (a *EchoLoggerAdapter) Debugf(format string, args ...any) {
	a.logger.Debug(fmt.Sprintf(format, args...))
}

// Debugj logs a JSON object at DEBUG level
func (a *EchoLoggerAdapter) Debugj(j echo_log.JSON) {
	a.logger.Debug("Echo JSON debug", Any("data", j))
}

// Info logs a message at INFO level
func (a *EchoLoggerAdapter) Info(i ...any) {
	a.logger.Info(fmt.Sprint(i...))
}

// Infof logs a formatted message at INFO level
func (a *EchoLoggerAdapter) Infof(format string, args ...any) {
	a.logger.Info(fmt.Sprintf(format, args...))
}

// Infoj logs a JSON object at INFO level
func (a *EchoLoggerAdapter) Infoj(j echo_log.JSON) {
	a.logger.Info("Echo JSON info", Any("data", j))
}

// Warn logs a message at WARN level
func (a *EchoLoggerAdapter) Warn(i ...any) {
	a.logger.Warn(fmt.Sprint(i...))
}

// Warnf logs a formatted message at WARN level
func (a *EchoLoggerAdapter) Warnf(format string, args ...any) {
	a.logger.Warn(fmt.Sprintf(format, args...))
}

// Warnj logs a JSON object at WARN level
func (a *EchoLoggerAdapter) Warnj(j echo_log.JSON) {
	a.logger.Warn("Echo JSON warn", Any("data", j))
}

// Error logs a message at ERROR level
func (a *EchoLoggerAdapter) Error(i ...any) {
	a.logger.Error(fmt.Sprint(i...))
}

// Errorf logs a formatted message at ERROR level
func (a *EchoLoggerAdapter) Errorf(format string, args ...any) {
	a.logger.Error(fmt.Sprintf(format, args...))
}

// Errorj logs a JSON object at ERROR level
func (a *EchoLoggerAdapter) Errorj(j echo_log.JSON) {
	a.logger.Error("Echo JSON error", Any("data", j))
}

// Fatal logs a message at ERROR level and panics to trigger graceful shutdown
func (a *EchoLoggerAdapter) Fatal(i ...any) {
	msg := fmt.Sprint(i...)
	a.logger.Error(msg)
	panic("Echo fatal error: " + msg)
}

// Fatalf logs a formatted message at ERROR level and panics
func (a *EchoLoggerAdapter) Fatalf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.logger.Error(msg)
	panic("Echo fatal error: " + msg)
}

// Fatalj logs a JSON object at ERROR level and panics
func (a *EchoLoggerAdapter) Fatalj(j echo_log.JSON) {
	a.logger.Error("Echo JSON fatal", Any("data", j))
	panic(fmt.Sprintf("Echo fatal error: %v", j))
}

// Panic logs a message at ERROR level and panics
func (a *EchoLoggerAdapter) Panic(i ...any) {
	msg := fmt.Sprint(i...)
	a.logger.Error(msg)
	panic(msg)
}

// Panicf logs a formatted message at ERROR level and panics
func (a *EchoLoggerAdapter) Panicf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.logger.Error(msg)
	panic(msg)
}

// Panicj logs a JSON object at ERROR level and panics
func (a *EchoLoggerAdapter) Panicj(j echo_log.JSON) {
	a.logger.Error("Echo JSON panic", Any("data", j))
	panic(j)
}
