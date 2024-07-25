// exported.go
package logger

import (
	"fmt"
	"os"
)

var defaultLogger *Logger

func SetupDefaultLogger(outputs map[string]LogOutput, prefix bool, rotationSettings Settings) {
	defaultLogger = NewLogger(outputs, prefix, rotationSettings)
}

func Info(channel, format string, a ...interface{}) {
	logWithLevel(INFO, channel, format, a...)
}

func Warn(channel, format string, a ...interface{}) {
	logWithLevel(WARNING, channel, format, a...)
}

func Error(channel, format string, a ...interface{}) {
	logWithLevel(ERROR, channel, format, a...)
}

func Debug(channel, format string, a ...interface{}) {
	logWithLevel(DEBUG, channel, format, a...)
}

func logWithLevel(level int, channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Log(channel, fmt.Sprintf(format, a...), level)
	} else {
		levelStr := [...]string{"INFO", "WARNING", "ERROR", "DEBUG"}[level]
		fmt.Fprintf(os.Stderr, "Default logger not initialized. Unable to log %s message: %s\n", levelStr, fmt.Sprintf(format, a...))
	}
}
