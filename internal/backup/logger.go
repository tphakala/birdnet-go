package backup

import "log"

// Logger is an interface for logging backup operations
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// defaultLogger implements the Logger interface using the standard log package
type defaultLogger struct {
	logger *log.Logger
}

// DefaultLogger returns a new default logger
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
