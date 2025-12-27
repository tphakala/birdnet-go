// Package analysis provides structured logging for the analysis package
package analysis

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// ComponentSoundLevel is the component identifier for sound level logging.
const ComponentSoundLevel = "analysis.soundlevel"

var (
	serviceLogger logger.Logger
	initOnce      sync.Once
)

// GetLogger returns the analysis package logger scoped to the analysis module.
// Uses sync.Once to ensure the logger is only initialized once.
func GetLogger() logger.Logger {
	initOnce.Do(func() {
		serviceLogger = logger.Global().Module("analysis")
	})
	return serviceLogger
}

// Sound level structured logging functions

// LogSoundLevelMQTTPublished logs successful MQTT publication of sound level data
func LogSoundLevelMQTTPublished(topic, source string, bandCount int) {
	GetLogger().Info("Published sound level data to MQTT",
		logger.String("topic", topic),
		logger.String("source", source),
		logger.Int("octave_bands", bandCount),
		logger.String("component", ComponentSoundLevel),
	)
}

// LogSoundLevelProcessorRegistered logs successful registration of sound level processor
func LogSoundLevelProcessorRegistered(source, sourceType, component string) {
	if component == "" {
		component = ComponentSoundLevel
	}
	GetLogger().Info("Registered sound level processor",
		logger.String("source", source),
		logger.String("source_type", sourceType),
		logger.String("component", component),
	)
}

// LogSoundLevelProcessorRegistrationFailed logs failed registration of sound level processor
func LogSoundLevelProcessorRegistrationFailed(source, sourceType, component string, err error) {
	if component == "" {
		component = ComponentSoundLevel
	}
	GetLogger().Error("Failed to register sound level processor",
		logger.String("source", source),
		logger.String("source_type", sourceType),
		logger.Error(err),
		logger.String("component", component),
	)
}

// LogSoundLevelProcessorUnregistered logs unregistration of sound level processor
func LogSoundLevelProcessorUnregistered(source, sourceType, component string) {
	if component == "" {
		component = ComponentSoundLevel
	}
	GetLogger().Info("Unregistered sound level processor",
		logger.String("source", source),
		logger.String("source_type", sourceType),
		logger.String("component", component),
	)
}

// LogSoundLevelRegistrationSummary logs the overall summary of sound level processor registrations
func LogSoundLevelRegistrationSummary(successCount, totalCount, activeStreams int, partialSuccess bool, errors []error) {
	switch {
	case successCount == totalCount:
		GetLogger().Info("Successfully registered all sound level processors",
			logger.Int("registered_processors", successCount),
			logger.Int("active_streams", activeStreams),
			logger.Bool("partial_success", false),
			logger.String("component", ComponentSoundLevel),
			logger.String("operation", "register_sound_level_processors"),
		)
	case successCount > 0:
		GetLogger().Warn("Partially registered sound level processors",
			logger.Int("successful_processors", successCount),
			logger.Int("total_processors", totalCount),
			logger.Int("failed_processors", totalCount-successCount),
			logger.Int("active_streams", activeStreams),
			logger.Bool("partial_success", true),
			logger.String("component", ComponentSoundLevel),
			logger.String("operation", "register_sound_level_processors"),
		)
		// Log first few errors for debugging
		for i, err := range errors {
			if i >= 3 {
				GetLogger().Warn("Additional sound level processor registration errors",
					logger.Int("remaining_errors", len(errors)-3),
					logger.String("component", ComponentSoundLevel),
					logger.String("operation", "register_sound_level_processors"),
				)
				break
			}
			GetLogger().Warn("Sound level processor registration error",
				logger.Int("error_number", i+1),
				logger.Error(err),
				logger.String("component", ComponentSoundLevel),
				logger.String("operation", "register_sound_level_processors"),
			)
		}
	default:
		GetLogger().Error("Failed to register any sound level processors",
			logger.Int("total_failures", len(errors)),
			logger.Bool("partial_success", false),
			logger.String("component", ComponentSoundLevel),
			logger.String("operation", "register_sound_level_processors"),
		)
	}
}

// LogSoundLevelActiveStreamNotInConfig logs when an active RTSP stream is not in configuration
func LogSoundLevelActiveStreamNotInConfig(url string) {
	GetLogger().Warn("Found active RTSP stream not in configuration",
		logger.String("rtsp_url", privacy.SanitizeRTSPUrl(url)),
		logger.String("component", ComponentSoundLevel),
	)
}
