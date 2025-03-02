// rotation.go
package logger

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// CreateRotatingFileCore creates a zapcore.Core that writes to a rotating file
func CreateRotatingFileCore(
	filePath string,
	encoder zapcore.Encoder,
	level zapcore.Level,
	rotationConfig RotationConfig,
) (zapcore.Core, error) {
	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// Configure the rotating logger
	w := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filePath,
		MaxSize:    rotationConfig.MaxSize,
		MaxBackups: rotationConfig.MaxBackups,
		MaxAge:     rotationConfig.MaxAge,
		Compress:   rotationConfig.Compress,
	})

	return zapcore.NewCore(encoder, w, zap.NewAtomicLevelAt(level)), nil
}

// NewRotatingLogger creates a new zap logger with file rotation
func NewRotatingLogger(
	config Config,
	rotationConfig RotationConfig,
) (*zap.Logger, error) {
	if config.FilePath == "" {
		return nil, errors.New("file path is required for rotating logger")
	}

	// Parse log level
	level := zapcore.InfoLevel
	if config.Development {
		log.Println("🚨 DEBUG: Development mode enabled")
		level = zapcore.DebugLevel
	} else {
		log.Println("🚨 DEBUG: Development mode disabled")
	}

	level = getZapLevel(config.Level, level)

	// Configure encoder
	encoderConfig := createEncoderConfig()

	// Always use non-color encoder for file-based rotating loggers
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var encoder zapcore.Encoder
	// For file output, respect ForceJSONFile setting
	if config.ForceJSONFile {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else if config.JSON {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// Create the core for file output
	core, err := CreateRotatingFileCore(config.FilePath, encoder, level, rotationConfig)
	if err != nil {
		return nil, err
	}

	// Create options
	opts := []zap.Option{
		zap.AddCaller(),
		zap.AddCallerSkip(1),
	}

	if config.Development {
		opts = append(opts, zap.Development())
	}

	// Return the logger
	return zap.New(core, opts...), nil
}

// NewRotatingCoreLogger creates a new Logger with file rotation
func NewRotatingCoreLogger(config Config, rotationConfig RotationConfig) (*Logger, error) {
	zapLogger, err := NewRotatingLogger(config, rotationConfig)
	if err != nil {
		return nil, err
	}

	return &Logger{
		zap:    zapLogger,
		sugar:  zapLogger.Sugar(),
		config: config,
	}, nil
}
