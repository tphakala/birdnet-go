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
	"time"

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

// createEncoderConfig creates a standard encoder configuration
func createEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     simpleDateTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

// simpleDateTimeEncoder encodes time as yyyy-MM-ddTHH:mm:ss without milliseconds or timezone
func simpleDateTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format("2006-01-02T15:04:05"))
}

// Debug logs a message at the debug level
func (l *Logger) Debug(msg string, fields ...interface{}) {
	l.sugar.Debugw(msg, fields...)
}

// Info logs a message at the info level
func (l *Logger) Info(msg string, fields ...interface{}) {
	l.sugar.Infow(msg, fields...)
}

// Warn logs a message at the warn level
func (l *Logger) Warn(msg string, fields ...interface{}) {
	l.sugar.Warnw(msg, fields...)
}

// Error logs a message at the error level
func (l *Logger) Error(msg string, fields ...interface{}) {
	l.sugar.Errorw(msg, fields...)
}

// Fatal logs a message at the fatal level and then calls os.Exit(1)
func (l *Logger) Fatal(msg string, fields ...interface{}) {
	l.sugar.Fatalw(msg, fields...)
}

// NewLogger creates a new logger with the given configuration.
// This is a unified initialization method for all logger types.
func NewLogger(config Config, rotationConfig ...RotationConfig) (*Logger, error) {
	// Log development mode status
	if config.Development {
		log.Println("🚨 Development mode enabled")
	}

	// Determine the log level
	level := zapcore.InfoLevel
	if config.Development {
		level = zapcore.DebugLevel
	}
	level = getZapLevel(config.Level, level)

	var zapLogger *zap.Logger

	// Create options
	opts := []zap.Option{}

	// Add caller information unless disabled
	if !config.DisableCaller {
		opts = append(opts, zap.AddCaller(), zap.AddCallerSkip(1))
	}

	if config.Development {
		opts = append(opts, zap.Development())
	}

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

		// Create console encoder config
		consoleEncoderConfig := createEncoderConfig()
		if !config.JSON && !config.DisableColor {
			consoleEncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		}

		// Create file encoder config
		fileEncoderConfig := createEncoderConfig()
		fileEncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

		// Create encoders
		var consoleEncoder, fileEncoder zapcore.Encoder
		if config.JSON {
			consoleEncoder = zapcore.NewJSONEncoder(consoleEncoderConfig)
			fileEncoder = zapcore.NewJSONEncoder(fileEncoderConfig)
		} else {
			consoleEncoder = zapcore.NewConsoleEncoder(consoleEncoderConfig)
			fileEncoder = zapcore.NewConsoleEncoder(fileEncoderConfig)
		}

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
		// Create encoder config
		encoderConfig := createEncoderConfig()
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder // No colors for file output

		// Create encoder
		var encoder zapcore.Encoder
		if config.JSON {
			encoder = zapcore.NewJSONEncoder(encoderConfig)
		} else {
			encoder = zapcore.NewConsoleEncoder(encoderConfig)
		}

		// Create the core
		core, coreErr := CreateRotatingFileCore(config.FilePath, encoder, level, rotationConfig[0])
		if coreErr != nil {
			return nil, coreErr
		}

		// Create logger
		zapLogger = zap.New(core, opts...)

	default:
		// Case 3: Simple logger (console only)
		// Create encoder config
		encoderConfig := createEncoderConfig()

		// For human-readable logs, use colored level if not disabled
		if !config.JSON && !config.DisableColor {
			encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		} else {
			encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		}

		// Create encoder
		var encoder zapcore.Encoder
		if config.JSON {
			encoder = zapcore.NewJSONEncoder(encoderConfig)
		} else {
			encoder = zapcore.NewConsoleEncoder(encoderConfig)
		}

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
