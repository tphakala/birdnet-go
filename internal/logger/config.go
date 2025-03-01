// config.go
package logger

import (
	"go.uber.org/zap/zapcore"
)

// Config holds the configuration for the logger
type Config struct {
	// Level is the minimum level logs will be written for
	Level string `json:"level"`
	// JSON enables structured JSON logging; when false, logs are in human-readable format
	JSON bool `json:"json"`
	// Development puts the logger in development mode, which changes the behavior of DPanicLevel
	Development bool `json:"development"`
	// FilePath is the path to the log file; if empty, logs go to stdout
	FilePath string `json:"file_path"`
	// DisableColor disables colored output for console logging
	DisableColor bool `json:"disable_color"`
	// DisableCaller disables including the calling function in the log output
	DisableCaller bool `json:"disable_caller"`
}

// DefaultConfig returns a default configuration for development
func DefaultConfig() Config {
	return Config{
		Level:         "",
		JSON:          false,
		Development:   true,
		FilePath:      "",
		DisableColor:  false,
		DisableCaller: false,
	}
}

// ProductionConfig returns a configuration suitable for production environments
func ProductionConfig() Config {
	return Config{
		Level:         "info",
		JSON:          true,
		Development:   false,
		FilePath:      "",
		DisableColor:  true,
		DisableCaller: false,
	}
}

// RotationConfig contains settings for log rotation
type RotationConfig struct {
	// MaxSize is the maximum size in megabytes of the log file before it gets rotated
	MaxSize int `json:"max_size_mb"`
	// MaxBackups is the maximum number of old log files to retain
	MaxBackups int `json:"max_backups"`
	// MaxAge is the maximum number of days to retain old log files
	MaxAge int `json:"max_age_days"`
	// Compress determines if the rotated log files should be compressed using gzip
	Compress bool `json:"compress"`
}

// DefaultRotationConfig returns a default configuration for log rotation
func DefaultRotationConfig() RotationConfig {
	return RotationConfig{
		MaxSize:    100, // 100 MB
		MaxBackups: 5,
		MaxAge:     30, // 30 days
		Compress:   true,
	}
}

// getZapLevel converts a level string to zapcore.Level
func getZapLevel(levelStr string, defaultLevel zapcore.Level) zapcore.Level {
	if levelStr == "" {
		return defaultLevel
	}

	level, err := zapcore.ParseLevel(levelStr)
	if err != nil {
		return defaultLevel
	}
	return level
}
