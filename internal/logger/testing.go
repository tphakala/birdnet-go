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
	// Determine log level
	level := zapcore.InfoLevel
	if config.Development {
		level = zapcore.DebugLevel
	}

	level = getZapLevel(config.Level, level)

	// Create encoder config
	encoderConfig := createEncoderConfig()

	// For human-readable logs, use colored level only if explicitly enabled
	// In tests, we default to no colors for better readability of test output
	if !config.JSON && !config.DisableColor {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	}

	// Create the encoder based on the encoding
	var encoder zapcore.Encoder
	if config.JSON {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// Set up output
	output := zapcore.AddSync(writer)

	return zapcore.NewCore(encoder, output, zap.NewAtomicLevelAt(level)), nil
}

// NewLoggerWithCore creates a new Logger with a custom core
// This is useful for testing to inject custom behavior
func NewLoggerWithCore(core zapcore.Core, config Config) *Logger {
	// Create options
	opts := []zap.Option{
		zap.AddCaller(),
		zap.AddCallerSkip(1),
	}

	if config.Development {
		opts = append(opts, zap.Development())
	}

	// Create the logger
	zapLogger := zap.New(core, opts...)
	sugar := zapLogger.Sugar()

	return &Logger{
		zap:    zapLogger,
		sugar:  sugar,
		config: config,
	}
}
