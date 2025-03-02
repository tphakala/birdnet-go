// logger.go
// Package logger provides a unified logging system built on top of zap.
//
// Key features:
// - Unified single-method initialization with NewLogger()
// - Support for console and file output with rotation
// - Development mode with debug level and additional information
// - Structured logging with fields
// - Automatic color support for console output
//
// Usage:
//
//	config := logger.Config{
//	    Level:       "info",
//	    Development: true,
//	    FilePath:    "/path/to/logfile.log",
//	}
//
//	// For console-only logging
//	log, err := logger.NewLogger(config)
//
//	// For file logging with rotation
//	rotationConfig := logger.RotationConfig{
//	    MaxSize:    100, // MB
//	    MaxBackups: 5,
//	    MaxAge:     30,  // days
//	    Compress:   true,
//	}
//	log, err := logger.NewLogger(config, rotationConfig)
package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger is the main logger struct that wraps zap.Logger
type Logger struct {
	zap    *zap.Logger
	sugar  *zap.SugaredLogger
	config Config
}

// Debug logs a message at the debug level
func (l *Logger) Debug(msg string, fields ...interface{}) {
	// Redact sensitive information from the message and fields
	safeMsg := RedactSensitiveData(msg)
	safeFields := RedactSensitiveFields(fields)
	l.sugar.Debugw(safeMsg, safeFields...)
}

// Info logs a message at the info level
func (l *Logger) Info(msg string, fields ...interface{}) {
	// Redact sensitive information from the message and fields
	safeMsg := RedactSensitiveData(msg)
	safeFields := RedactSensitiveFields(fields)
	l.sugar.Infow(safeMsg, safeFields...)
}

// Warn logs a message at the warn level
func (l *Logger) Warn(msg string, fields ...interface{}) {
	// Redact sensitive information from the message and fields
	safeMsg := RedactSensitiveData(msg)
	safeFields := RedactSensitiveFields(fields)
	l.sugar.Warnw(safeMsg, safeFields...)
}

// Error logs a message at the error level
func (l *Logger) Error(msg string, fields ...interface{}) {
	// Redact sensitive information from the message and fields
	safeMsg := RedactSensitiveData(msg)
	safeFields := RedactSensitiveFields(fields)
	l.sugar.Errorw(safeMsg, safeFields...)
}

// Fatal logs a message at the fatal level and then calls os.Exit(1)
func (l *Logger) Fatal(msg string, fields ...interface{}) {
	// Redact sensitive information from the message and fields
	safeMsg := RedactSensitiveData(msg)
	safeFields := RedactSensitiveFields(fields)
	l.sugar.Fatalw(safeMsg, safeFields...)
}

// NewLogger creates a new logger with the given configuration.
// This is a unified initialization method for all logger types.
func NewLogger(config Config, rotationConfig ...RotationConfig) (*Logger, error) {
	// Log development mode status
	if config.Development {
		log.Println("🚨 Development mode enabled")
	}

	// Determine the log level using the helper function
	level := GetLogLevel(config)

	var zapLogger *zap.Logger

	// Get options from config
	opts := GetZapOptions(config)

	// Determine logger type based on configuration
	switch {
	case config.FilePath != "" && config.Development:
		// Case 1: Development mode with file path - use tee logger (console AND file)
		// Always prioritize development mode with tee logger when file path is provided
		// Default rotation config if not provided
		rc := DefaultRotationConfig()
		if len(rotationConfig) > 0 {
			rc = rotationConfig[0]
		}

		// Get encoder configs for console and file
		consoleEncoderConfig := GetEncoderConfig(config, true) // true for console
		fileEncoderConfig := GetEncoderConfig(config, false)   // false for file

		// Create encoders
		consoleEncoder := CreateEncoder(config, consoleEncoderConfig)
		fileEncoder := CreateEncoder(config, fileEncoderConfig)

		// Console output
		consoleOutput := zapcore.AddSync(os.Stdout)

		// Ensure directory exists
		dir := filepath.Dir(config.FilePath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// File output with rotation
		fileOutput := zapcore.AddSync(&lumberjack.Logger{
			Filename:   config.FilePath,
			MaxSize:    rc.MaxSize,
			MaxBackups: rc.MaxBackups,
			MaxAge:     rc.MaxAge,
			Compress:   rc.Compress,
		})

		// Create cores
		consoleCore := zapcore.NewCore(consoleEncoder, consoleOutput, zap.NewAtomicLevelAt(level))
		fileCore := zapcore.NewCore(fileEncoder, fileOutput, zap.NewAtomicLevelAt(level))

		// Combine cores
		core := zapcore.NewTee(consoleCore, fileCore)
		zapLogger = zap.New(core, opts...)

	case config.FilePath != "" && len(rotationConfig) > 0:
		// Case 2: File output with rotation configuration (non-development mode)
		// Get encoder config for file
		encoderConfig := GetEncoderConfig(config, false) // false for file

		// Create encoder
		encoder := CreateEncoder(config, encoderConfig)

		// Create the core
		core, coreErr := CreateRotatingFileCore(config.FilePath, encoder, level, rotationConfig[0])
		if coreErr != nil {
			return nil, coreErr
		}

		// Create logger
		zapLogger = zap.New(core, opts...)

	default:
		// Case 3: Simple logger (console only)
		// Get encoder config for console
		encoderConfig := GetEncoderConfig(config, true) // true for console

		// Create encoder
		encoder := CreateEncoder(config, encoderConfig)

		// Create output
		output := zapcore.AddSync(os.Stdout)

		// Create core
		core := zapcore.NewCore(encoder, output, zap.NewAtomicLevelAt(level))
		zapLogger = zap.New(core, opts...)
	}

	return &Logger{
		zap:    zapLogger,
		sugar:  zapLogger.Sugar(),
		config: config,
	}, nil
}

// Named returns a new logger with the given name
func (l *Logger) Named(name string) *Logger {
	return &Logger{
		zap:    l.zap.Named(name),
		sugar:  l.zap.Named(name).Sugar(),
		config: l.config,
	}
}

// With returns a new logger with the given fields added to the logging context
func (l *Logger) With(fields ...interface{}) *Logger {
	return &Logger{
		zap:    l.zap,
		sugar:  l.sugar.With(fields...),
		config: l.config,
	}
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.zap.Sync()
}

// GetZapLogger returns the underlying zap.Logger
// Useful if you need to use Zap directly
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.zap
}
