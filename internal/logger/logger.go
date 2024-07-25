// logger.go
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

type Log struct {
	Level   int
	Time    time.Time
	Message string
}

type Logger struct {
	Outputs map[string]LogOutput
	Prefix  bool
}

type LogOutput interface {
	WriteLog(log Log, prefix bool)
}

type StdoutOutput struct{}

func (s StdoutOutput) WriteLog(log Log, prefix bool) {
	logMessage := formatLog(log, prefix)
	fmt.Fprint(os.Stdout, logMessage)
}

type FileOutput struct {
	Handler FileHandler
}

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

func formatLog(log Log, prefix bool) string {
	formattedMessage := log.Message
	if !strings.HasSuffix(formattedMessage, "\n") {
		formattedMessage += "\n"
	}

	if prefix {
		level := [...]string{"INFO", "WARNING", "ERROR", "DEBUG"}[log.Level]
		return fmt.Sprintf("[%s] [%s] %s", log.Time.Format(time.RFC3339), level, formattedMessage)
	}
	return formattedMessage
}

func NewLogger(outputs map[string]LogOutput, prefix bool, rotationSettings Settings) *Logger {
	for _, output := range outputs {
		if fileOutput, ok := output.(FileOutput); ok {
			if defaultHandler, ok := fileOutput.Handler.(*DefaultFileHandler); ok {
				defaultHandler.settings = rotationSettings
			}
		}
	}

	return &Logger{
		Outputs: outputs,
		Prefix:  prefix,
	}
}

func (l *Logger) Write(p []byte) (n int, err error) {
	message := string(p)
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

func (l *Logger) log(channel string, level int, format string, a ...interface{}) {
	message := fmt.Sprintf(format, a...)
	l.Log(channel, message, level)
}
