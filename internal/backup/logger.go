package backup

import "log"

// Logger is an interface for logging backup operations
//
// Deprecated: This interface is being phased out in favor of *slog.Logger
// for structured logging. New code should use slog.Logger directly.
// This interface remains for backward compatibility with existing target
// implementations (ftp.go, gdrive.go, rsync.go) that haven't been migrated yet.
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// defaultLogger implements the Logger interface using the standard log package
type defaultLogger struct {
	logger *log.Logger
}

// DefaultLogger returns a new default logger
//
// Deprecated: Use slog.Default() or create a new *slog.Logger instead.
func DefaultLogger() Logger {
	return &defaultLogger{
		logger: log.Default(),
	}
}

// Printf implements Logger.Printf
func (l *defaultLogger) Printf(format string, v ...interface{}) {
	l.logger.Printf(format, v...)
}

// Println implements Logger.Println
func (l *defaultLogger) Println(v ...interface{}) {
	l.logger.Println(v...)
}
