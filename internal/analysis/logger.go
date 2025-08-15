// Package analysis provides structured logging for the analysis package
package analysis

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Package-level logger for analysis operations
var (
	logger         *slog.Logger
	loggerInitOnce sync.Once
	levelVar       = new(slog.LevelVar) // Dynamic level control
	closeLogger    func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "analysis.log")
	initialLevel := slog.LevelInfo // Default to Info level
	levelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	logger, closeLogger, err = logging.NewFileLogger(logFilePath, "analysis", levelVar)
	if err != nil {
		// Fallback: Log error to standard log and use console logging
		log.Printf("Failed to initialize analysis file logger at %s: %v. Using console logging.", logFilePath, err)
		// Set logger to console handler for actual console output
		fbHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar})
		logger = slog.New(fbHandler).With("service", "analysis")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// GetLogger returns the package logger for use in subpackages
// This allows other analysis subpackages to use the same logger
// if they don't need their own dedicated logger. Thread-safe initialization
// is guaranteed through sync.Once.
func GetLogger() *slog.Logger {
	loggerInitOnce.Do(func() {
		if logger == nil {
			logger = slog.Default().With("service", "analysis")
		}
	})
	return logger
}

// CloseLogger closes the log file and releases resources
func CloseLogger() error {
	if closeLogger != nil {
		return closeLogger()
	}
	return nil
}

// Sound level structured logging functions

// LogSoundLevelMQTTPublished logs successful MQTT publication of sound level data
func LogSoundLevelMQTTPublished(topic, source string, bandCount int) {
	GetLogger().Info("Published sound level data to MQTT",
		"topic", topic,
		"source", source,
		"octave_bands", bandCount,
		"component", "analysis.soundlevel",
	)
}

// LogSoundLevelProcessorRegistered logs successful registration of sound level processor
func LogSoundLevelProcessorRegistered(source, sourceType, component string) {
	if component == "" {
		component = "analysis.soundlevel"
	}
	GetLogger().Info("Registered sound level processor",
		"source", source,
		"source_type", sourceType,
		"component", component,
	)
}

// LogSoundLevelProcessorRegistrationFailed logs failed registration of sound level processor
func LogSoundLevelProcessorRegistrationFailed(source, sourceType, component string, err error) {
	if component == "" {
		component = "analysis.soundlevel"
	}
	GetLogger().Error("Failed to register sound level processor",
		"source", source,
		"source_type", sourceType,
		"error", err,
		"component", component,
	)
}

// LogSoundLevelProcessorUnregistered logs unregistration of sound level processor
func LogSoundLevelProcessorUnregistered(source, sourceType, component string) {
	if component == "" {
		component = "analysis.soundlevel"
	}
	GetLogger().Info("Unregistered sound level processor",
		"source", source,
		"source_type", sourceType,
		"component", component,
	)
}

// LogSoundLevelRegistrationSummary logs the overall summary of sound level processor registrations
func LogSoundLevelRegistrationSummary(successCount, totalCount, activeStreams int, partialSuccess bool, errors []error) {
	switch {
	case successCount == totalCount:
		GetLogger().Info("Successfully registered all sound level processors",
			"registered_processors", successCount,
			"active_streams", activeStreams,
			"partial_success", false,
			"component", "analysis.soundlevel",
			"operation", "register_sound_level_processors",
		)
	case successCount > 0:
		GetLogger().Warn("Partially registered sound level processors",
			"successful_processors", successCount,
			"total_processors", totalCount,
			"failed_processors", totalCount-successCount,
			"active_streams", activeStreams,
			"partial_success", true,
			"component", "analysis.soundlevel",
			"operation", "register_sound_level_processors",
		)
		// Log first few errors for debugging
		for i, err := range errors {
			if i >= 3 {
				GetLogger().Warn("Additional sound level processor registration errors",
					"remaining_errors", len(errors)-3,
					"component", "analysis.soundlevel",
					"operation", "register_sound_level_processors",
				)
				break
			}
			GetLogger().Warn("Sound level processor registration error",
				"error_number", i+1,
				"error", err,
				"component", "analysis.soundlevel",
				"operation", "register_sound_level_processors",
			)
		}
	default:
		GetLogger().Error("Failed to register any sound level processors",
			"total_failures", len(errors),
			"partial_success", false,
			"component", "analysis.soundlevel",
			"operation", "register_sound_level_processors",
		)
	}
}

// LogSoundLevelActiveStreamNotInConfig logs when an active RTSP stream is not in configuration
func LogSoundLevelActiveStreamNotInConfig(url string) {
	GetLogger().Warn("Found active RTSP stream not in configuration",
		"rtsp_url", privacy.SanitizeRTSPUrl(url),
		"component", "analysis.soundlevel",
	)
}
