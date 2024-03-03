package logger

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Log level constants.
const (
	INFO = iota
	WARNING
	ERROR
	DEBUG
)

// Log represents a single log entry.
type Log struct {
	Level   int       // Log level
	Time    time.Time // Time of log entry
	Message string    // Log message
}

// Logger manages logging to various outputs.
type Logger struct {
	Outputs map[string]LogOutput // Mapping of log channels to their outputs
	Prefix  bool                 // Indicates if log messages should be prefixed with metadata
}

// LogOutput defines an interface for log outputs.
type LogOutput interface {
	WriteLog(log Log, prefix bool)
}

// StdoutOutput writes logs to stdout.
type StdoutOutput struct{}

// WriteLog writes a log entry to stdout.
func (s StdoutOutput) WriteLog(log Log, prefix bool) {
	logMessage := formatLog(log, prefix)
	fmt.Fprint(os.Stdout, logMessage)
}

// FileOutput writes logs to a file.
type FileOutput struct {
	Handler FileHandler // File handler for writing logs
}

// WriteLog writes a log entry to a file.
func (f FileOutput) WriteLog(log Log, prefix bool) {
	if f.Handler == nil {
		fmt.Println("File handler not initialized")
		return
	}
	logMessage := formatLog(log, prefix)
	_, err := f.Handler.Write([]byte(logMessage))
	if err != nil {
		fmt.Printf("Error writing log: %s\n", err)
	}
}

// formatLog formats a log entry based on its level and prefix requirement.
func formatLog(log Log, prefix bool) string {
	formattedMessage := log.Message
	if !strings.HasSuffix(formattedMessage, "\n") {
		formattedMessage += "\n"
	}

	var logMessage string
	if prefix {
		// Prepend log level and timestamp if prefix is enabled
		var level string
		switch log.Level {
		case INFO:
			level = "INFO"
		case WARNING:
			level = "WARNING"
		case ERROR:
			level = "ERROR"
		case DEBUG:
			level = "DEBUG"
		}
		logMessage = fmt.Sprintf("[%s] [%s] %s", log.Time.Format(time.RFC3339), level, formattedMessage)
	} else {
		logMessage = formattedMessage
	}

	return logMessage
}

// NewLogger creates a new Logger instance.
func NewLogger(outputs map[string]LogOutput, prefix bool) *Logger {
	return &Logger{
		Outputs: outputs,
		Prefix:  prefix,
	}
}

// Write allows Logger to comply with io.Writer interface, enabling compatibility with Go's standard logging utilities.
func (l *Logger) Write(p []byte) (n int, err error) {
	message := string(p)
	// Default log level for io.Writer interface is INFO
	for _, output := range l.Outputs {
		log := Log{
			Level:   INFO,
			Time:    time.Now(),
			Message: message,
		}
		output.WriteLog(log, l.Prefix)
	}

	return len(p), nil
}

// Log sends a log entry to a specific channel.
func (l *Logger) Log(channel, message string, level int) {
	if output, exists := l.Outputs[channel]; exists {
		log := Log{
			Level:   level,
			Time:    time.Now(),
			Message: message,
		}
		output.WriteLog(log, l.Prefix)
	} else {
		// Handle unknown log channels
		fmt.Fprintf(os.Stderr, "Unknown log channel: %s\n", channel)
	}
}

// Helper functions for logging at specific levels.
func (l *Logger) Info(channel, format string, a ...interface{}) {
	l.log(channel, INFO, format, a...)
}

func (l *Logger) Warning(channel, format string, a ...interface{}) {
	l.log(channel, WARNING, format, a...)
}

func (l *Logger) Error(channel, format string, a ...interface{}) {
	l.log(channel, ERROR, format, a...)
}

func (l *Logger) Debug(channel, format string, a ...interface{}) {
	l.log(channel, DEBUG, format, a...)
}

// log is a helper function to format and log a message.
func (l *Logger) log(channel string, level int, format string, a ...interface{}) {
	message := fmt.Sprintf(format, a...)
	log := Log{
		Level:   level,
		Time:    time.Now(),
		Message: message,
	}

	if output, exists := l.Outputs[channel]; exists {
		output.WriteLog(log, l.Prefix)
	} else {
		// Handle unknown log channels
		fmt.Fprintf(os.Stderr, "Unknown log channel: %s\n", channel)
	}
}
