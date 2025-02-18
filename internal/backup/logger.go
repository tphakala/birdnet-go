package backup

import "log"

// Logger defines the interface for logging in the backup package
type Logger interface {
	Printf(format string, v ...interface{})
	Println(v ...interface{})
}

// defaultLogger implements Logger using the standard log package
type defaultLogger struct {
	*log.Logger
}

// DefaultLogger returns a default logger implementation
func DefaultLogger() Logger {
	return &defaultLogger{
		Logger: log.Default(),
	}
}

// Printf implements Logger.Printf
func (l *defaultLogger) Printf(format string, v ...interface{}) {
	l.Logger.Printf(format, v...)
}

// Println implements Logger.Println
func (l *defaultLogger) Println(v ...interface{}) {
	l.Logger.Println(v...)
}
