package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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

// getSoundLevelLogger returns the sound level logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getSoundLevelLogger() logger.Logger {
	return logger.Global().Module("analysis").Module("soundlevel")
}

// sanitizeSoundLevelData replaces non-finite float values (Inf, -Inf, NaN) with valid placeholders
// and polishes the data to ensure JSON marshaling succeeds. This prevents errors when publishing
// to MQTT, SSE, or other systems that require valid JSON.
func sanitizeSoundLevelData(data myaudio.SoundLevelData) myaudio.SoundLevelData {
	// Create a copy to avoid modifying the original
	sanitized := myaudio.SoundLevelData{
		Timestamp:   data.Timestamp,
		Source:      stringOrDefault(data.Source, "unknown"),
		Name:        stringOrDefault(data.Name, "unknown"),
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
				lg := getSoundLevelLogger()
				lg.Debug("sanitized and polished octave band data",
					logger.String("original_band", key),
					logger.String("normalized_band", normalizedKey),
					logger.Float64("center_freq", band.CenterFreq),
					logger.Float64("original_min", band.Min),
					logger.Float64("original_max", band.Max),
					logger.Float64("original_mean", band.Mean),
					logger.Float64("sanitized_min", sanitizedBand.Min),
					logger.Float64("sanitized_max", sanitizedBand.Max),
					logger.Float64("sanitized_mean", sanitizedBand.Mean))
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

// stringOrDefault returns the value if non-empty, otherwise returns the default.
// This is a simple helper for providing fallback values, not for security sanitization.
func stringOrDefault(value, defaultValue string) string {
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
					lg := getSoundLevelLogger()
					lg.Debug("received sound level data",
						logger.String("source", soundData.Source),
						logger.String("name", soundData.Name),
						logger.Time("timestamp", soundData.Timestamp),
						logger.Int("duration", soundData.Duration),
						logger.Int("bands_count", len(soundData.OctaveBands)))
				}
				// Publish sound level data to MQTT
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					getSoundLevelLogger().Error("Failed to publish sound level data to MQTT",
						logger.Error(err),
						logger.String("source", soundData.Source),
						logger.String("name", soundData.Name))
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
			lg := getSoundLevelLogger()
			lg.Debug("sound level data validation failed",
				logger.String("source", soundData.Source),
				logger.Error(err))
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
		lg := getSoundLevelLogger()
		lg.Debug("published sound level data to MQTT",
			logger.String("topic", topic),
			logger.String("source", soundData.Source),
			logger.String("name", soundData.Name),
			logger.Int("json_size", len(jsonData)),
			logger.Int("octave_bands", len(soundData.OctaveBands)),
			logger.String("component", "analysis.soundlevel"),
			logger.String("operation", "publish_mqtt"))

		// Log each octave band's values only if realtime logging is enabled
		if settings.Realtime.Audio.SoundLevel.DebugRealtimeLogging {
			for band, data := range sanitizedData.OctaveBands {
				lg.Debug("octave band values",
					logger.String("band", band),
					logger.Float64("center_freq", data.CenterFreq),
					logger.Float64("min_db", data.Min),
					logger.Float64("max_db", data.Max),
					logger.Float64("mean_db", data.Mean),
					logger.Int("sample_count", data.SampleCount),
					logger.String("component", "analysis.soundlevel"),
					logger.String("operation", "log_octave_bands"))
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
					lg := getSoundLevelLogger()
					lg.Debug("MQTT publisher received sound level data",
						logger.String("source", soundData.Source),
						logger.String("name", soundData.Name),
						logger.Time("timestamp", soundData.Timestamp))
				}
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					// Log with enhanced error (error already has telemetry context from publishSoundLevelToMQTT)
					getSoundLevelLogger().Error("Failed to publish sound level data to MQTT",
						logger.Error(err),
						logger.String("source", soundData.Source),
						logger.String("name", soundData.Name))
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
			lg := getSoundLevelLogger()
			lg.Debug("sound level data validation failed for SSE",
				logger.String("source", soundData.Source),
				logger.Error(err))
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
		lg := getSoundLevelLogger()
		lg.Debug("successfully broadcast sound level data via SSE",
			logger.String("source", soundData.Source),
			logger.String("name", soundData.Name),
			logger.Int("bands_count", len(soundData.OctaveBands)))
	}

	return nil
}

// startSoundLevelMetricsPublisherWithDone starts metrics publisher with a custom done channel
func startSoundLevelMetricsPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, metricsInstance *observability.Metrics, soundLevelChan chan myaudio.SoundLevelData) {
	lg := getSoundLevelLogger()
	wg.Go(func() {
		lg.Info("started sound level metrics publisher")

		for {
			select {
			case <-doneChan:
				lg.Info("stopping sound level metrics publisher")
				return
			case soundData := <-soundLevelChan:
				// Log received sound level data if debug is enabled
				if conf.Setting().Realtime.Audio.SoundLevel.Debug {
					lg := getSoundLevelLogger()
					lg.Debug("metrics publisher received sound level data",
						logger.String("source", soundData.Source),
						logger.String("name", soundData.Name),
						logger.Time("timestamp", soundData.Timestamp))
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
	activeStreams := myaudio.GetStreamHealth()

	// Register for each configured RTSP source, but prioritize actually running streams
	configuredURLs := make(map[string]bool)
	for _, stream := range settings.Realtime.RTSP.Streams {
		configuredURLs[stream.URL] = true
		totalSources++

		// Get or create the stream source in the registry with display name
		// This ensures the stream has the correct user-friendly name for logs and MQTT
		registry := myaudio.GetRegistry()
		audioSource := registry.GetOrCreateSourceWithName(stream.URL, myaudio.StreamTypeToSourceType(stream.Type), stream.Name)
		if audioSource == nil {
			errs = append(errs, errors.Newf("failed to get/create stream source").
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "get_or_create_stream_source").
				Context("stream_name", stream.Name).
				Context("url", privacy.SanitizeStreamUrl(stream.URL)).
				Build())
			LogSoundLevelProcessorRegistrationFailed(stream.Name, stream.Type, "analysis.soundlevel", fmt.Errorf("failed to get/create stream source"))
			continue
		}

		if err := myaudio.RegisterSoundLevelProcessor(audioSource.ID, audioSource.DisplayName); err != nil {
			// Safely check stream health status
			var streamRunning bool
			var streamExists bool
			if streamHealth, exists := activeStreams[stream.URL]; exists {
				streamRunning = streamHealth.IsHealthy
				streamExists = true
			}

			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", string(audioSource.Type)).
				Context("source_id", audioSource.ID).
				Context("display_name", audioSource.DisplayName).
				Context("source_url", stream.URL).
				Context("stream_running", streamRunning).
				Context("stream_exists", streamExists). // indicates if stream was found in health map
				Build())
			LogSoundLevelProcessorRegistrationFailed(audioSource.DisplayName, string(audioSource.Type)+"_stream", "analysis.soundlevel", err)
		} else {
			successCount++
			if _, isActive := activeStreams[stream.URL]; isActive {
				LogSoundLevelProcessorRegistered(audioSource.DisplayName, string(audioSource.Type)+"_active", "analysis.soundlevel")
			} else {
				LogSoundLevelProcessorRegistered(audioSource.DisplayName, string(audioSource.Type)+"_configured", "analysis.soundlevel")
			}
		}
	}

	// Warn about active streams that aren't configured (shouldn't normally happen)
	for url := range activeStreams {
		if !configuredURLs[url] {
			LogSoundLevelActiveStreamNotInConfig(privacy.SanitizeStreamUrl(url))
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
			if audioSource, exists := registry.GetSourceByConnection(settings.Realtime.Audio.Source); exists {
				myaudio.UnregisterSoundLevelProcessor(audioSource.ID)
				LogSoundLevelProcessorUnregistered(audioSource.DisplayName, "audio_device", "analysis.soundlevel")
			}
			// If source doesn't exist, nothing to unregister - this is expected during teardown
		} else {
			GetLogger().Warn("registry not available during sound level processor unregistration")
		}
	}

	// Unregister all stream sources
	for _, stream := range settings.Realtime.RTSP.Streams {
		// Get the source from registry to retrieve its ID
		registry := myaudio.GetRegistry()
		if registry != nil {
			if audioSource, exists := registry.GetSourceByConnection(stream.URL); exists {
				myaudio.UnregisterSoundLevelProcessor(audioSource.ID)
				LogSoundLevelProcessorUnregistered(stream.Name, "stream", "analysis.soundlevel")
			}
			// If source doesn't exist, nothing to unregister - this is expected during teardown
		}
	}
}
