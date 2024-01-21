package logger

// exported.go file

var defaultLogger *Logger

func SetupDefaultLogger(outputs map[string]LogOutput, prefix bool) {
	defaultLogger = NewLogger(outputs, prefix)
}

func Info(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Info(channel, format, a...)
	}
}

func Warn(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Warning(channel, format, a...)
	}
}

func Error(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Error(channel, format, a...)
	}
}

func Debug(channel, format string, a ...interface{}) {
	if defaultLogger != nil {
		defaultLogger.Debug(channel, format, a...)
	}
}
