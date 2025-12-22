package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for sound level monitoring
const (
	// dB value bounds for validation and sanitization
	minValidDB = -200.0
	maxValidDB = 20.0

	// Error message constants
	errMsgEmptyField       = "empty %s field"
	errMsgInvalidTimestamp = "invalid timestamp: %s"
	errMsgOutOfRange       = "dB values out of range in octave band %s"
	errMsgNonFiniteValues  = "non-finite values in octave band %s"
	errMsgNoOctaveBandData = "no octave band data"
)

// Package-level logger for sound level monitoring
var (
	soundLevelLogger    *slog.Logger
	soundLoggerOnce     sync.Once
	serviceLevelVar     = new(slog.LevelVar) // Dynamic level control
	soundLevelCloseFunc func() error
)

// getSoundLevelLogger returns the sound level logger, initializing it if necessary
func getSoundLevelLogger() *slog.Logger {
	soundLoggerOnce.Do(func() {
		var err error
		// Define log file path relative to working directory
		logFilePath := filepath.Join("logs", "soundlevel.log")
		// Set initial level based on debug flag
		initialLevel := slog.LevelInfo
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			initialLevel = slog.LevelDebug
		}
		serviceLevelVar.Set(initialLevel)

		// Initialize the service-specific file logger
		soundLevelLogger, soundLevelCloseFunc, err = logging.NewFileLogger(logFilePath, "analysis.soundlevel", serviceLevelVar)
		if err != nil {
			// Fallback: Use main analysis logger and log the issue
			mainLogger := GetLogger() // Get the main analysis logger
			if mainLogger != nil {
				soundLevelLogger = mainLogger.With("subsystem", "sound-level")
				soundLevelLogger.Warn("Failed to initialize sound level file logger, using fallback",
					"error", err,
					"log_path", logFilePath,
					"service", "analysis.soundlevel")
			} else {
				// Ultimate fallback to default logger if even the main logger isn't available
				soundLevelLogger = slog.Default().With("service", "analysis.soundlevel")
			}
			soundLevelCloseFunc = func() error { return nil } // No-op closer
		}
	})
	return soundLevelLogger
}

// getSoundLevelServiceLevelVar returns the service level var for dynamic log level control
func getSoundLevelServiceLevelVar() *slog.LevelVar {
	return serviceLevelVar
}

// CloseSoundLevelLogger closes the sound level file logger and releases resources
// This should be called during component shutdown to ensure proper cleanup of file handles
func CloseSoundLevelLogger() error {
	if soundLevelCloseFunc != nil {
		return soundLevelCloseFunc()
	}
	return nil
}

// sanitizeSoundLevelData replaces non-finite float values (Inf, -Inf, NaN) with valid placeholders
// and polishes the data to ensure JSON marshaling succeeds. This prevents errors when publishing
// to MQTT, SSE, or other systems that require valid JSON.
func sanitizeSoundLevelData(data myaudio.SoundLevelData) myaudio.SoundLevelData {
	// Create a copy to avoid modifying the original
	sanitized := myaudio.SoundLevelData{
		Timestamp:   data.Timestamp,
		Source:      sanitizeString(data.Source, "unknown"),
		Name:        sanitizeString(data.Name, "unknown"),
		Duration:    data.Duration,
		OctaveBands: make(map[string]myaudio.OctaveBandData),
	}

	// Ensure duration is valid
	if sanitized.Duration <= 0 {
		sanitized.Duration = 10 // Default to 10 seconds
	}

	// Sanitize and polish each octave band
	for key, band := range data.OctaveBands {
		// Round all dB values to 2 decimal places for cleaner output
		sanitizedBand := myaudio.OctaveBandData{
			CenterFreq:  band.CenterFreq,
			Min:         roundToDecimalPlaces(sanitizeFloat64(band.Min, -100.0), 2),
			Max:         roundToDecimalPlaces(sanitizeFloat64(band.Max, -100.0), 2),
			Mean:        roundToDecimalPlaces(sanitizeFloat64(band.Mean, -100.0), 2),
			SampleCount: band.SampleCount,
		}

		// Ensure logical consistency: min <= mean <= max
		if sanitizedBand.Min > sanitizedBand.Mean {
			sanitizedBand.Min = sanitizedBand.Mean
		}
		if sanitizedBand.Max < sanitizedBand.Mean {
			sanitizedBand.Max = sanitizedBand.Mean
		}

		// Normalize band key format (ensure consistent naming)
		normalizedKey := normalizeBandKey(key)
		sanitized.OctaveBands[normalizedKey] = sanitizedBand

		// Log sanitization details if debug is enabled and realtime logging is on
		if conf.Setting().Realtime.Audio.SoundLevel.Debug && conf.Setting().Realtime.Audio.SoundLevel.DebugRealtimeLogging {
			if band.Min != sanitizedBand.Min || band.Max != sanitizedBand.Max ||
				band.Mean != sanitizedBand.Mean || key != normalizedKey {
				if logger := getSoundLevelLogger(); logger != nil {
					logger.Debug("sanitized and polished octave band data",
						"original_band", key,
						"normalized_band", normalizedKey,
						"center_freq", band.CenterFreq,
						"original_min", band.Min,
						"original_max", band.Max,
						"original_mean", band.Mean,
						"sanitized_min", sanitizedBand.Min,
						"sanitized_max", sanitizedBand.Max,
						"sanitized_mean", sanitizedBand.Mean)
				}
			}
		}
	}

	return sanitized
}

// sanitizeFloat64 replaces non-finite float values with a default value
func sanitizeFloat64(value, defaultValue float64) float64 {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		return defaultValue
	}
	// Clamp to reasonable bounds for sound levels
	if value < -200.0 {
		return -200.0
	}
	if value > 20.0 {
		return 20.0
	}
	return value
}

// sanitizeString ensures a string is not empty
func sanitizeString(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

// roundToDecimalPlaces rounds a float64 to the specified number of decimal places
func roundToDecimalPlaces(value float64, places int) float64 {
	factor := math.Pow(10, float64(places))
	return math.Round(value*factor) / factor
}

// normalizeBandKey ensures consistent band key formatting
func normalizeBandKey(key string) string {
	// Already normalized keys will pass through unchanged
	// Convert any variations to consistent format
	if strings.Contains(key, "_") {
		return key // Already in correct format
	}

	// Handle "1000Hz" -> "1.0_kHz" conversion
	key = strings.ToLower(key)
	if before, ok := strings.CutSuffix(key, "hz"); ok {
		freq := before
		if val, err := strconv.ParseFloat(freq, 64); err == nil {
			if val >= 1000 {
				return fmt.Sprintf("%.1f_kHz", val/1000)
			}
			return fmt.Sprintf("%.1f_Hz", val)
		}
	}

	return key // Return as-is if we can't parse it
}

// validateSoundLevelData validates sound level data before publishing
func validateSoundLevelData(data *myaudio.SoundLevelData) error {
	// Common sound data context for error reporting
	soundDataCtx := map[string]any{
		"source": data.Source,
		"name":   data.Name,
	}

	// Check timestamp is valid and not in the future
	if data.Timestamp.IsZero() {
		return errors.Newf(errMsgInvalidTimestamp, "zero time").
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "validate_timestamp").
			Context("sound_data", soundDataCtx).
			Context("retryable", false). // Input validation errors are not retryable
			Build()
	}

	// Use a single time reference to avoid drift between validation and context
	currentTime := time.Now()
	if data.Timestamp.After(currentTime.Add(5 * time.Minute)) {
		return errors.Newf(errMsgInvalidTimestamp, "future time").
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "validate_timestamp").
			Context("timestamp", data.Timestamp).
			Context("current_time", currentTime).
			Context("sound_data", soundDataCtx).
			Context("retryable", false). // Input validation errors are not retryable
			Build()
	}

	// Verify source and name are non-empty
	if data.Source == "" {
		return errors.Newf(errMsgEmptyField, "source").
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "validate_fields").
			Context("field", "source").
			Context("name", data.Name).
			Build()
	}

	if data.Name == "" {
		return errors.Newf(errMsgEmptyField, "name").
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "validate_fields").
			Context("field", "name").
			Context("source", data.Source).
			Build()
	}

	// Ensure at least one octave band has data
	if len(data.OctaveBands) == 0 {
		return errors.Newf(errMsgNoOctaveBandData).
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "validate_octave_bands").
			Context("sound_data", soundDataCtx).
			Context("duration", data.Duration).
			Build()
	}

	// Verify all dB values are within reasonable range
	for band, bandData := range data.OctaveBands {
		if math.IsNaN(bandData.Min) || math.IsInf(bandData.Min, 0) ||
			math.IsNaN(bandData.Max) || math.IsInf(bandData.Max, 0) ||
			math.IsNaN(bandData.Mean) || math.IsInf(bandData.Mean, 0) {
			return errors.Newf(errMsgNonFiniteValues, band).
				Component("analysis.soundlevel").
				Category(errors.CategorySoundLevel).
				Context("operation", "validate_octave_bands").
				Context("band", band).
				Context("center_freq", bandData.CenterFreq).
				Context("min_value", bandData.Min).
				Context("max_value", bandData.Max).
				Context("mean_value", bandData.Mean).
				Context("sound_data", soundDataCtx).
				Build()
		}

		// Check reasonable bounds (minValidDB to maxValidDB)
		if bandData.Min < minValidDB || bandData.Min > maxValidDB ||
			bandData.Max < minValidDB || bandData.Max > maxValidDB ||
			bandData.Mean < minValidDB || bandData.Mean > maxValidDB {
			return errors.Newf(errMsgOutOfRange, band).
				Component("analysis.soundlevel").
				Category(errors.CategorySoundLevel).
				Context("operation", "validate_octave_bands").
				Context("band", band).
				Context("center_freq", bandData.CenterFreq).
				Context("min_value", bandData.Min).
				Context("max_value", bandData.Max).
				Context("mean_value", bandData.Mean).
				Context("valid_range_min", minValidDB).
				Context("valid_range_max", maxValidDB).
				Context("sound_data", soundDataCtx).
				Build()
		}
	}

	return nil
}

// CompactSoundLevelData is a space-efficient version for MQTT publishing
type CompactSoundLevelData struct {
	TS    string                     `json:"ts"`   // ISO8601 timestamp
	Node  string                     `json:"node"` // Node name (BirdNET-Go instance)
	Src   string                     `json:"src"`  // Source
	Name  string                     `json:"nm"`   // Name
	Dur   int                        `json:"dur"`  // Duration in seconds
	Bands map[string]CompactBandData `json:"b"`    // Octave bands
}

// CompactBandData is a compact representation of octave band data
type CompactBandData struct {
	Freq float64 `json:"f"` // Center frequency
	Min  float64 `json:"n"` // Min dB (1 decimal)
	Max  float64 `json:"x"` // Max dB (1 decimal)
	Mean float64 `json:"m"` // Mean dB (1 decimal)
}

// toCompactFormat converts sound level data to compact format for MQTT
func toCompactFormat(data myaudio.SoundLevelData, nodeName string) CompactSoundLevelData {
	compact := CompactSoundLevelData{
		TS:    data.Timestamp.Format(time.RFC3339),
		Node:  nodeName,
		Src:   data.Source,
		Name:  data.Name,
		Dur:   data.Duration,
		Bands: make(map[string]CompactBandData),
	}

	// Convert bands to compact format with 1 decimal place
	for band, bandData := range data.OctaveBands {
		compact.Bands[band] = CompactBandData{
			Freq: bandData.CenterFreq,
			Min:  roundToDecimalPlaces(bandData.Min, 1),
			Max:  roundToDecimalPlaces(bandData.Max, 1),
			Mean: roundToDecimalPlaces(bandData.Mean, 1),
		}
	}

	return compact
}

// startSoundLevelMQTTPublisher starts a goroutine to consume sound level data and publish to MQTT
func startSoundLevelMQTTPublisher(wg *sync.WaitGroup, quitChan <-chan struct{}, proc *processor.Processor, soundLevelChan <-chan myaudio.SoundLevelData) {
	wg.Go(func() {

		for {
			select {
			case <-quitChan:
				return
			case soundData, ok := <-soundLevelChan:
				if !ok {
					// Channel is closed, exit gracefully
					getSoundLevelLogger().Info("Sound level channel closed, stopping MQTT publisher")
					return
				}
				// Log received sound level data if debug is enabled
				// This is logged at interval rate, not realtime
				if conf.Setting().Realtime.Audio.SoundLevel.Debug {
					if logger := getSoundLevelLogger(); logger != nil {
						logger.Debug("received sound level data",
							"source", soundData.Source,
							"name", soundData.Name,
							"timestamp", soundData.Timestamp,
							"duration", soundData.Duration,
							"bands_count", len(soundData.OctaveBands))
					}
				}
				// Publish sound level data to MQTT
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					getSoundLevelLogger().Error("Failed to publish sound level data to MQTT",
						"error", err,
						"source", soundData.Source,
						"name", soundData.Name)
				}
			}
		}
	})
}

// publishSoundLevelToMQTT publishes sound level data to MQTT
func publishSoundLevelToMQTT(soundData myaudio.SoundLevelData, proc *processor.Processor) error {
	// Get current settings to determine MQTT topic
	settings := conf.Setting()
	if !settings.Realtime.MQTT.Enabled {
		return nil // MQTT not enabled, skip
	}

	// Validate data before processing
	if err := validateSoundLevelData(&soundData); err != nil {
		// Log validation error if debug enabled
		if settings.Realtime.Audio.SoundLevel.Debug {
			if logger := getSoundLevelLogger(); logger != nil {
				logger.Debug("sound level data validation failed",
					"source", soundData.Source,
					"error", err)
			}
		}
		return err
	}

	// Create MQTT topic for sound level data
	topic := fmt.Sprintf("%s/soundlevel", strings.TrimSuffix(settings.Realtime.MQTT.Topic, "/"))

	// Sanitize sound level data before JSON marshaling
	sanitizedData := sanitizeSoundLevelData(soundData)

	// Convert to compact format for MQTT to reduce payload size
	compactData := toCompactFormat(sanitizedData, settings.Main.Name)

	// Marshal compact sound level data to JSON
	jsonData, err := json.Marshal(compactData)
	if err != nil {
		// Record error metric
		if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
			proc.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "mqtt", "marshal_error")
		}
		return errors.New(err).
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "marshal_compact_data").
			Context("source", soundData.Source).
			Context("name", soundData.Name).
			Context("octave_bands_count", len(compactData.Bands)).
			Build()
	}

	// Publish to MQTT
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := proc.PublishMQTT(ctx, topic, string(jsonData)); err != nil {
		// Record error metric
		if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
			proc.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "mqtt", "publish_error")
			proc.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "mqtt", "error")
		}
		return errors.New(err).
			Component("analysis.soundlevel").
			Category(errors.CategorySoundLevel).
			Context("operation", "publish_mqtt").
			Context("topic", topic).
			Context("source", soundData.Source).
			Context("name", soundData.Name).
			Context("payload_size", len(jsonData)).
			Context("timeout_seconds", 5).
			Context("octave_bands_count", len(compactData.Bands)).
			Context("retryable", true). // MQTT publish failures are typically retryable
			Build()
	}

	// Record success metric
	if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		proc.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "mqtt", "success")
	}

	LogSoundLevelMQTTPublished(topic, soundData.Source, len(soundData.OctaveBands))

	// Log detailed sound level data if debug is enabled
	// These logs are for publishing events, not realtime processing
	if settings.Realtime.Audio.SoundLevel.Debug {
		if logger := getSoundLevelLogger(); logger != nil {
			logger.Debug("published sound level data to MQTT",
				"topic", topic,
				"source", soundData.Source,
				"name", soundData.Name,
				"json_size", len(jsonData),
				"octave_bands", len(soundData.OctaveBands),
				"component", "analysis.soundlevel",
				"operation", "publish_mqtt")

			// Log each octave band's values only if realtime logging is enabled
			if settings.Realtime.Audio.SoundLevel.DebugRealtimeLogging {
				for band, data := range sanitizedData.OctaveBands {
					logger.Debug("octave band values",
						"band", band,
						"center_freq", data.CenterFreq,
						"min_db", data.Min,
						"max_db", data.Max,
						"mean_db", data.Mean,
						"sample_count", data.SampleCount,
						"component", "analysis.soundlevel",
						"operation", "log_octave_bands")
				}
			}
		}
	}

	return nil
}

// startSoundLevelPublishers starts all sound level publishers with the given done channel
func startSoundLevelPublishers(wg *sync.WaitGroup, doneChan chan struct{}, proc *processor.Processor, soundLevelChan chan myaudio.SoundLevelData, apiController *apiv2.Controller) {
	settings := conf.Setting()

	// Create a merged quit channel that responds to both the done channel and global quit
	mergedQuitChan := make(chan struct{})
	go func() {
		<-doneChan
		close(mergedQuitChan)
	}()

	// Start MQTT publisher if enabled
	if settings.Realtime.MQTT.Enabled {
		startSoundLevelMQTTPublisherWithDone(wg, mergedQuitChan, proc, soundLevelChan)
	}

	// Start SSE publisher if API is available
	if apiController != nil {
		startSoundLevelSSEPublisherWithDone(wg, mergedQuitChan, apiController, soundLevelChan)
	}

	// Start metrics publisher
	if proc != nil && proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		startSoundLevelMetricsPublisherWithDone(wg, mergedQuitChan, proc.Metrics, soundLevelChan)
	}
}

// startSoundLevelMQTTPublisherWithDone starts MQTT publisher with a custom done channel
func startSoundLevelMQTTPublisherWithDone(wg *sync.WaitGroup, doneChan <-chan struct{}, proc *processor.Processor, soundLevelChan <-chan myaudio.SoundLevelData) {
	wg.Go(func() {
		getSoundLevelLogger().Info("Started sound level MQTT publisher")

		for {
			select {
			case <-doneChan:
				getSoundLevelLogger().Info("Stopping sound level MQTT publisher")
				return
			case soundData, ok := <-soundLevelChan:
				if !ok {
					// Channel is closed, exit gracefully
					getSoundLevelLogger().Info("Sound level channel closed, stopping MQTT publisher")
					return
				}
				// Log received sound level data if debug is enabled
				if conf.Setting().Realtime.Audio.SoundLevel.Debug {
					if logger := getSoundLevelLogger(); logger != nil {
						logger.Debug("MQTT publisher received sound level data",
							"source", soundData.Source,
							"name", soundData.Name,
							"timestamp", soundData.Timestamp)
					}
				}
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					// Log with enhanced error (error already has telemetry context from publishSoundLevelToMQTT)
					getSoundLevelLogger().Error("Failed to publish sound level data to MQTT",
						"error", err,
						"source", soundData.Source,
						"name", soundData.Name)
				}
			}
		}
	})
}

// startSoundLevelSSEPublisherWithDone starts SSE publisher with a custom done channel
// This is a compatibility wrapper that converts done channel to context for the refactored function
func startSoundLevelSSEPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, apiController *apiv2.Controller, soundLevelChan chan myaudio.SoundLevelData) {
	// Create context that gets canceled when done channel is closed
	ctx, cancel := context.WithCancel(context.Background())

	// Convert done channel to context cancellation
	go func() {
		select {
		case <-doneChan:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Call the refactored function with context and receive-only channel
	startSoundLevelSSEPublisher(wg, ctx, apiController, soundLevelChan)
}

// broadcastSoundLevelSSE broadcasts sound level data via SSE with error handling and metrics
func broadcastSoundLevelSSE(apiController *apiv2.Controller, soundData myaudio.SoundLevelData) error {
	// Validate data before broadcasting
	if err := validateSoundLevelData(&soundData); err != nil {
		// Log validation error if debug enabled
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			if logger := getSoundLevelLogger(); logger != nil {
				logger.Debug("sound level data validation failed for SSE",
					"source", soundData.Source,
					"error", err)
			}
		}
		return err
	}

	// Sanitize data before broadcasting
	sanitizedData := sanitizeSoundLevelData(soundData)

	if err := apiController.BroadcastSoundLevel(&sanitizedData); err != nil {
		// Record error metric
		if m := getSoundLevelMetrics(apiController); m != nil {
			m.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "sse", "broadcast_error")
			m.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "error")
		}

		// Return enhanced error
		return errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "broadcast_sound_level_sse").
			Context("source", soundData.Source).
			Context("name", soundData.Name).
			Context("bands_count", len(soundData.OctaveBands)).
			Build()
	}

	// Record success metric
	if m := getSoundLevelMetrics(apiController); m != nil {
		m.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "success")
	}

	// Log successful broadcast if debug is enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Debug {
		if logger := getSoundLevelLogger(); logger != nil {
			logger.Debug("successfully broadcast sound level data via SSE",
				"source", soundData.Source,
				"name", soundData.Name,
				"bands_count", len(soundData.OctaveBands))
		}
	}

	return nil
}

// startSoundLevelMetricsPublisherWithDone starts metrics publisher with a custom done channel
func startSoundLevelMetricsPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, metricsInstance *observability.Metrics, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Go(func() {
		log.Println("📊 Started sound level metrics publisher")

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level metrics publisher")
				return
			case soundData := <-soundLevelChan:
				// Log received sound level data if debug is enabled
				if conf.Setting().Realtime.Audio.SoundLevel.Debug {
					if logger := getSoundLevelLogger(); logger != nil {
						logger.Debug("metrics publisher received sound level data",
							"source", soundData.Source,
							"name", soundData.Name,
							"timestamp", soundData.Timestamp)
					}
				}
				// Update Prometheus metrics
				if metricsInstance != nil && metricsInstance.SoundLevel != nil {
					updateSoundLevelMetrics(soundData, metricsInstance)
				}
			}
		}
	})
}

// registerSoundLevelProcessorsForActiveSources registers sound level processors for all active audio sources
func registerSoundLevelProcessorsForActiveSources(settings *conf.Settings) error {
	var errs []error
	successCount := 0
	totalSources := 0

	// Register for audio device source if active
	if settings.Realtime.Audio.Source != "" {
		totalSources++
		// Get or create the audio source in the registry
		registry := myaudio.GetRegistry()
		audioSource := registry.GetOrCreateSource(settings.Realtime.Audio.Source, myaudio.SourceTypeAudioCard)
		if audioSource == nil {
			errs = append(errs, errors.Newf("failed to get/create audio source").
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "get_or_create_audio_source").
				Context("source", settings.Realtime.Audio.Source).
				Build())
			LogSoundLevelProcessorRegistrationFailed(settings.Realtime.Audio.Source, "audio_device", "analysis.soundlevel", fmt.Errorf("failed to get/create audio source"))
		} else if err := myaudio.RegisterSoundLevelProcessor(audioSource.ID, audioSource.DisplayName); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "audio_device").
				Context("source_id", audioSource.ID).
				Context("display_name", audioSource.DisplayName).
				Build())
			LogSoundLevelProcessorRegistrationFailed(audioSource.DisplayName, "audio_device", "analysis.soundlevel", err)
		} else {
			successCount++
			LogSoundLevelProcessorRegistered(audioSource.DisplayName, "audio_device", "analysis.soundlevel")
		}
	}

	// Get actually running RTSP streams to ensure we only register for active streams
	activeStreams := myaudio.GetRTSPStreamHealth()

	// Register for each configured RTSP source, but prioritize actually running streams
	configuredURLs := make(map[string]bool)
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = true
		totalSources++

		// Get or create the RTSP source in the registry
		registry := myaudio.GetRegistry()
		audioSource := registry.GetOrCreateSource(url, myaudio.SourceTypeRTSP)
		if audioSource == nil {
			errs = append(errs, errors.Newf("failed to get/create RTSP source").
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "get_or_create_rtsp_source").
				Context("url", privacy.SanitizeRTSPUrl(url)).
				Build())
			LogSoundLevelProcessorRegistrationFailed(privacy.SanitizeRTSPUrl(url), "rtsp", "analysis.soundlevel", fmt.Errorf("failed to get/create RTSP source"))
			continue
		}

		if err := myaudio.RegisterSoundLevelProcessor(audioSource.ID, audioSource.DisplayName); err != nil {
			// Safely check stream health status
			var streamRunning bool
			var streamExists bool
			if streamHealth, exists := activeStreams[url]; exists {
				streamRunning = streamHealth.IsHealthy
				streamExists = true
			}

			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "rtsp").
				Context("source_id", audioSource.ID).
				Context("display_name", audioSource.DisplayName).
				Context("source_url", url).
				Context("stream_running", streamRunning).
				Context("stream_exists", streamExists). // indicates if stream was found in health map
				Build())
			LogSoundLevelProcessorRegistrationFailed(audioSource.DisplayName, "rtsp_stream", "analysis.soundlevel", err)
		} else {
			successCount++
			if _, isActive := activeStreams[url]; isActive {
				LogSoundLevelProcessorRegistered(audioSource.DisplayName, "rtsp_active", "analysis.soundlevel")
			} else {
				LogSoundLevelProcessorRegistered(audioSource.DisplayName, "rtsp_configured", "analysis.soundlevel")
			}
		}
	}

	// Warn about active streams that aren't configured (shouldn't normally happen)
	for url := range activeStreams {
		if !configuredURLs[url] {
			LogSoundLevelActiveStreamNotInConfig(privacy.SanitizeRTSPUrl(url))
		}
	}

	// Use structured logging for registration summary
	LogSoundLevelRegistrationSummary(successCount, totalSources, len(activeStreams), successCount > 0 && successCount < totalSources, errs)

	// Return error only if we have complete failure
	// For partial success, we continue operating with available processors
	if successCount == 0 && len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// unregisterAllSoundLevelProcessors unregisters all sound level processors
func unregisterAllSoundLevelProcessors(settings *conf.Settings) {
	// Unregister audio source
	if settings.Realtime.Audio.Source != "" {
		// Get the audio source from registry instead of hardcoded "malgo"
		registry := myaudio.GetRegistry()
		if registry != nil {
			if audioSource := registry.GetOrCreateSource(settings.Realtime.Audio.Source, myaudio.SourceTypeAudioCard); audioSource != nil {
				myaudio.UnregisterSoundLevelProcessor(audioSource.ID)
				LogSoundLevelProcessorUnregistered(audioSource.DisplayName, "audio_device", "analysis.soundlevel")
			} else {
				log.Printf("⚠️ Failed to get audio source from registry during sound level processor unregistration")
			}
		} else {
			log.Printf("⚠️ Registry not available during sound level processor unregistration")
		}
	}

	// Unregister all RTSP sources
	for _, url := range settings.Realtime.RTSP.URLs {
		myaudio.UnregisterSoundLevelProcessor(url)
		LogSoundLevelProcessorUnregistered(privacy.SanitizeRTSPUrl(url), "rtsp_stream", "analysis.soundlevel")
	}
}
