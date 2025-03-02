// testing.go
package logger

import (
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// CreateTestCore creates a zapcore.Core that writes to the given io.Writer
// This is useful for testing to intercept logger output
func CreateTestCore(config Config, writer io.Writer) (zapcore.Core, error) {
	// Determine log level using helper function
	level := GetLogLevel(config)

	// Get encoder config
	encoderConfig := GetEncoderConfig(config, true) // Use console settings for tests

	// Create the encoder
	encoder := CreateEncoder(config, encoderConfig)

	// Set up output
	output := zapcore.AddSync(writer)

	return zapcore.NewCore(encoder, output, zap.NewAtomicLevelAt(level)), nil
}

// NewLoggerWithCore creates a new Logger with a custom core
// This is useful for testing to inject custom behavior
func NewLoggerWithCore(core zapcore.Core, config Config) *Logger {
	// Get options from config
	opts := GetZapOptions(config)

	// Create the logger
	zapLogger := zap.New(core, opts...)
	sugar := zapLogger.Sugar()

	return &Logger{
		zap:    zapLogger,
		sugar:  sugar,
		config: config,
	}
}
