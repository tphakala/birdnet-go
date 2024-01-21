package logger

// logger.go file

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Log levels constants.
const (
	INFO = iota
	WARNING
	ERROR
	DEBUG
)

// Log represents a single log entry.
type Log struct {
	Level   int
	Time    time.Time
	Message string
}

// Logger represents a logging instance with multiple channels.
type Logger struct {
	Outputs map[string]LogOutput
	Prefix  bool
}

// LogOutput defines the interface for log outputs.
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
	Handler FileHandler
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

// formatLog formats the log entry for output.
func formatLog(log Log, prefix bool) string {
	formattedMessage := log.Message
	if !strings.HasSuffix(formattedMessage, "\n") {
		formattedMessage += "\n"
	}

	var logMessage string

	if prefix {
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

func NewLogger(outputs map[string]LogOutput, prefix bool) *Logger {
	return &Logger{
		Outputs: outputs,
		Prefix:  prefix,
	}
}

// Write implements the io.Writer interface.
// It allows the logger to be used with standard Go logging utilities.
func (l *Logger) Write(p []byte) (n int, err error) {
	message := string(p)

	// Here you can decide how to handle the log message.
	// For simplicity, let's log it to all outputs as INFO.
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

// Log logs a message to a specific channel.
func (l *Logger) Log(channel, message string, level int) {
	if output, exists := l.Outputs[channel]; exists {
		log := Log{
			Level:   level,
			Time:    time.Now(),
			Message: message,
		}
		output.WriteLog(log, l.Prefix)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown log channel: %s\n", channel)
	}
}

// Info logs an informational message.
func (l *Logger) Info(channel, format string, a ...interface{}) {
	l.log(channel, INFO, format, a...)
}

// Warning logs a warning message.
func (l *Logger) Warning(channel, format string, a ...interface{}) {
	l.log(channel, WARNING, format, a...)
}

// Error logs an error message.
func (l *Logger) Error(channel, format string, a ...interface{}) {
	l.log(channel, ERROR, format, a...)
}

// Debug logs a debug message.
func (l *Logger) Debug(channel, format string, a ...interface{}) {
	l.log(channel, DEBUG, format, a...)
}

// log is a helper function to format and log the message.
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
		fmt.Fprintf(os.Stderr, "Unknown log channel: %s\n", channel)
	}
}
