// config.go
package logger

import (
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config holds the configuration for the logger
type Config struct {
	// Level is the minimum level logs will be written for
	Level string `json:"level"`
	// JSON enables structured JSON logging; when false, logs are in human-readable format
	JSON bool `json:"json"`
	// ForceJSONFile forces JSON format for file output, regardless of the JSON setting
	ForceJSONFile bool `json:"force_json_file"`
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
		ForceJSONFile: false,
		Development:   true,
		FilePath:      "",
		DisableColor:  false,
		DisableCaller: true,
	}
}

// ProductionConfig returns a configuration suitable for production environments
func ProductionConfig() Config {
	return Config{
		Level:         "info",
		JSON:          true,
		ForceJSONFile: true,
		Development:   false,
		FilePath:      "",
		DisableColor:  true,
		DisableCaller: true,
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

// createEncoderConfig creates a standard encoder configuration
func createEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller", // This will be overridden in GetEncoderConfig if DisableCaller is true
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

// GetZapOptions returns the zap options based on the given configuration
func GetZapOptions(config Config) []zap.Option {
	opts := []zap.Option{}

	// Add caller information unless disabled
	if !config.DisableCaller {
		opts = append(opts, zap.AddCaller(), zap.AddCallerSkip(1))
	}

	if config.Development {
		opts = append(opts, zap.Development())
	}

	return opts
}

// GetLogLevel determines the appropriate log level based on configuration
func GetLogLevel(config Config) zapcore.Level {
	// Set default level based on mode
	level := zapcore.InfoLevel
	if config.Development {
		level = zapcore.DebugLevel
	}

	// Override with explicit level if provided
	return getZapLevel(config.Level, level)
}

// GetEncoderConfig returns the appropriate encoder configuration
// isConsole indicates if this is for console output (vs file output)
func GetEncoderConfig(config Config, isConsole bool) zapcore.EncoderConfig {
	encoderConfig := createEncoderConfig()

	// If caller is disabled, remove CallerKey from the encoder config
	if config.DisableCaller {
		encoderConfig.CallerKey = ""
	}

	// Apply color to console output if enabled
	if isConsole && !config.JSON && !config.DisableColor {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	}

	return encoderConfig
}

// CreateEncoder creates the appropriate encoder based on configuration
func CreateEncoder(config Config, encoderConfig zapcore.EncoderConfig, isConsole bool) zapcore.Encoder {
	// For file output, respect ForceJSONFile setting
	if !isConsole && config.ForceJSONFile {
		return zapcore.NewJSONEncoder(encoderConfig)
	}

	// Otherwise use the JSON setting
	if config.JSON {
		return zapcore.NewJSONEncoder(encoderConfig)
	}
	return zapcore.NewConsoleEncoder(encoderConfig)
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

// DualLogConfig returns a configuration for human-readable console logs and structured JSON file logs.
// This is a helper function to easily set up a dual-format logging configuration where:
// - Console output is human-readable for better developer experience
// - File output is JSON formatted for better machine parsing and log analysis
//
// The returned config will have:
// - Level set to "info"
// - JSON set to false (human-readable console logs)
// - ForceJSONFile set to true (structured JSON file logs)
// - Development mode disabled
// - Colors enabled for console output
// - Caller information disabled
func DualLogConfig(filePath string) Config {
	return Config{
		Level:         "info",
		JSON:          false, // Human-readable for console
		ForceJSONFile: true,  // Force JSON for file
		Development:   false,
		FilePath:      filePath,
		DisableColor:  false,
		DisableCaller: true,
	}
}
