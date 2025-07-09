package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"golang.org/x/time/rate"
)

// Package-level logger for sound level monitoring
var (
	soundLevelLogger *slog.Logger
	soundLoggerOnce  sync.Once
	serviceLevelVar  = new(slog.LevelVar) // Dynamic level control
	closeLogger      func() error
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
		soundLevelLogger, closeLogger, err = logging.NewFileLogger(logFilePath, "sound-level", serviceLevelVar)
		if err != nil {
			// Fallback: Log error to standard log and use stdout logger
			log.Printf("WARNING: Failed to initialize sound level file logger at %s: %v. Using console logging.", logFilePath, err)
			// Fallback to console logger
			logging.Init()
			fbHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: serviceLevelVar})
			soundLevelLogger = slog.New(fbHandler).With("service", "sound-level")
			closeLogger = func() error { return nil } // No-op closer
		}
	})
	return soundLevelLogger
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
	if strings.HasSuffix(key, "hz") {
		freq := strings.TrimSuffix(key, "hz")
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
	// Check timestamp is valid and not in the future
	if data.Timestamp.IsZero() {
		return errors.New(fmt.Errorf("invalid timestamp: zero time")).
			Component("realtime-analysis").
			Category(errors.CategoryValidation).
			Context("operation", "validate_sound_level_data").
			Build()
	}

	if data.Timestamp.After(time.Now().Add(5 * time.Minute)) {
		return errors.New(fmt.Errorf("invalid timestamp: future time")).
			Component("realtime-analysis").
			Category(errors.CategoryValidation).
			Context("operation", "validate_sound_level_data").
			Context("timestamp", data.Timestamp).
			Build()
	}

	// Verify source and name are non-empty
	if data.Source == "" {
		return errors.New(fmt.Errorf("empty source field")).
			Component("realtime-analysis").
			Category(errors.CategoryValidation).
			Context("operation", "validate_sound_level_data").
			Build()
	}

	if data.Name == "" {
		return errors.New(fmt.Errorf("empty name field")).
			Component("realtime-analysis").
			Category(errors.CategoryValidation).
			Context("operation", "validate_sound_level_data").
			Build()
	}

	// Ensure at least one octave band has data
	if len(data.OctaveBands) == 0 {
		return errors.New(fmt.Errorf("no octave band data")).
			Component("realtime-analysis").
			Category(errors.CategoryValidation).
			Context("operation", "validate_sound_level_data").
			Context("source", data.Source).
			Build()
	}

	// Verify all dB values are within reasonable range
	for band, bandData := range data.OctaveBands {
		if math.IsNaN(bandData.Min) || math.IsInf(bandData.Min, 0) ||
			math.IsNaN(bandData.Max) || math.IsInf(bandData.Max, 0) ||
			math.IsNaN(bandData.Mean) || math.IsInf(bandData.Mean, 0) {
			return errors.Newf("non-finite values in band %s", band).
				Component("realtime-analysis").
				Category(errors.CategoryValidation).
				Context("operation", "validate_sound_level_data").
				Context("band", band).
				Build()
		}

		// Check reasonable bounds (-200 to +20 dB)
		if bandData.Min < -200 || bandData.Min > 20 ||
			bandData.Max < -200 || bandData.Max > 20 ||
			bandData.Mean < -200 || bandData.Mean > 20 {
			return errors.Newf("dB values out of range in band %s", band).
				Component("realtime-analysis").
				Category(errors.CategoryValidation).
				Context("operation", "validate_sound_level_data").
				Context("band", band).
				Context("min", bandData.Min).
				Context("max", bandData.Max).
				Context("mean", bandData.Mean).
				Build()
		}
	}

	return nil
}

// CompactSoundLevelData is a space-efficient version for MQTT publishing
type CompactSoundLevelData struct {
	TS    string                     `json:"ts"`  // ISO8601 timestamp
	Src   string                     `json:"src"` // Source
	Name  string                     `json:"nm"`  // Name
	Dur   int                        `json:"dur"` // Duration in seconds
	Bands map[string]CompactBandData `json:"b"`   // Octave bands
}

// CompactBandData is a compact representation of octave band data
type CompactBandData struct {
	Freq float64 `json:"f"` // Center frequency
	Min  float64 `json:"n"` // Min dB (1 decimal)
	Max  float64 `json:"x"` // Max dB (1 decimal)
	Mean float64 `json:"m"` // Mean dB (1 decimal)
}

// toCompactFormat converts sound level data to compact format for MQTT
func toCompactFormat(data myaudio.SoundLevelData) CompactSoundLevelData {
	compact := CompactSoundLevelData{
		TS:    data.Timestamp.Format(time.RFC3339),
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
func startSoundLevelMQTTPublisher(wg *sync.WaitGroup, quitChan chan struct{}, proc *processor.Processor) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-quitChan:
				return
			case soundData := <-soundLevelChan:
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
					log.Printf("❌ Error publishing sound level data to MQTT: %v", err)
				}
			}
		}
	}()
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
	compactData := toCompactFormat(sanitizedData)

	// Marshal compact sound level data to JSON
	jsonData, err := json.Marshal(compactData)
	if err != nil {
		// Record error metric
		if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
			proc.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "mqtt", "marshal_error")
		}
		return errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "marshal_sound_level_data").
			Context("source", soundData.Source).
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
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "publish_sound_level_mqtt").
			Context("topic", topic).
			Context("source", soundData.Source).
			Build()
	}

	// Record success metric
	if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		proc.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "mqtt", "success")
	}

	log.Printf("📡 Published sound level data to MQTT topic: %s (source: %s, bands: %d)",
		topic, soundData.Source, len(soundData.OctaveBands))

	// Log detailed sound level data if debug is enabled
	// These logs are for publishing events, not realtime processing
	if settings.Realtime.Audio.SoundLevel.Debug {
		if logger := getSoundLevelLogger(); logger != nil {
			logger.Debug("published sound level data to MQTT",
				"topic", topic,
				"source", soundData.Source,
				"name", soundData.Name,
				"json_size", len(jsonData),
				"octave_bands", len(soundData.OctaveBands))

			// Log each octave band's values only if realtime logging is enabled
			if settings.Realtime.Audio.SoundLevel.DebugRealtimeLogging {
				for band, data := range sanitizedData.OctaveBands {
					logger.Debug("octave band values",
						"band", band,
						"center_freq", data.CenterFreq,
						"min_db", data.Min,
						"max_db", data.Max,
						"mean_db", data.Mean,
						"sample_count", data.SampleCount)
				}
			}
		}
	}

	return nil
}

// startSoundLevelPublishers starts all sound level publishers with the given done channel
func startSoundLevelPublishers(wg *sync.WaitGroup, doneChan chan struct{}, proc *processor.Processor, soundLevelChan chan myaudio.SoundLevelData, httpServer *httpcontroller.Server) {
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
	if httpServer != nil && httpServer.APIV2 != nil {
		startSoundLevelSSEPublisherWithDone(wg, mergedQuitChan, httpServer.APIV2, soundLevelChan)
	}

	// Start metrics publisher
	if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		startSoundLevelMetricsPublisherWithDone(wg, mergedQuitChan, proc.Metrics, soundLevelChan)
	}
}

// startSoundLevelMQTTPublisherWithDone starts MQTT publisher with a custom done channel
func startSoundLevelMQTTPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, proc *processor.Processor, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("📡 Started sound level MQTT publisher")

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level MQTT publisher")
				return
			case soundData := <-soundLevelChan:
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
					log.Printf("❌ Error publishing sound level data to MQTT: %v", err)
				}
			}
		}
	}()
}

// startSoundLevelSSEPublisherWithDone starts SSE publisher with a custom done channel
func startSoundLevelSSEPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, apiController *api.Controller, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("📡 Started sound level SSE publisher")

		// Create a rate limiter: 1 log per minute
		errorLogLimiter := rate.NewLimiter(rate.Every(time.Minute), 1)

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level SSE publisher")
				return
			case soundData := <-soundLevelChan:
				// Log received sound level data if debug is enabled
				if conf.Setting().Realtime.Audio.SoundLevel.Debug {
					if logger := getSoundLevelLogger(); logger != nil {
						logger.Debug("SSE publisher received sound level data",
							"source", soundData.Source,
							"name", soundData.Name,
							"timestamp", soundData.Timestamp)
					}
				}
				if err := broadcastSoundLevelSSE(apiController, soundData); err != nil {
					// Only log errors if rate limiter allows
					if errorLogLimiter.Allow() {
						log.Printf("⚠️ Error broadcasting sound level data via SSE: %v", err)
					}
				}
			}
		}
	}()
}

// broadcastSoundLevelSSE broadcasts sound level data via SSE with error handling and metrics
func broadcastSoundLevelSSE(apiController *api.Controller, soundData myaudio.SoundLevelData) error {
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
		if m := getSoundLevelMetricsFromAPI(apiController); m != nil {
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
	if m := getSoundLevelMetricsFromAPI(apiController); m != nil {
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

// getSoundLevelMetricsFromAPI safely retrieves sound level metrics from API controller
func getSoundLevelMetricsFromAPI(apiController *api.Controller) *metrics.SoundLevelMetrics {
	if apiController == nil || apiController.Processor == nil ||
		apiController.Processor.Metrics == nil || apiController.Processor.Metrics.SoundLevel == nil {
		return nil
	}
	return apiController.Processor.Metrics.SoundLevel
}

// startSoundLevelMetricsPublisherWithDone starts metrics publisher with a custom done channel
func startSoundLevelMetricsPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, metricsInstance *observability.Metrics, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	}()
}

// registerSoundLevelProcessorsForActiveSources registers sound level processors for all active audio sources
func registerSoundLevelProcessorsForActiveSources(settings *conf.Settings) error {
	var errs []error
	successCount := 0
	totalSources := 0

	// Register for malgo source if active
	if settings.Realtime.Audio.Source != "" {
		totalSources++
		if err := myaudio.RegisterSoundLevelProcessor("malgo", settings.Realtime.Audio.Source); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "malgo").
				Context("source_name", settings.Realtime.Audio.Source).
				Build())
			log.Printf("❌ Failed to register sound level processor for audio device %s: %v", settings.Realtime.Audio.Source, err)
		} else {
			successCount++
			log.Printf("🔊 Registered sound level processor for audio device: %s", settings.Realtime.Audio.Source)
		}
	}

	// Register for each RTSP source
	for _, url := range settings.Realtime.RTSP.URLs {
		totalSources++
		displayName := conf.SanitizeRTSPUrl(url)
		if err := myaudio.RegisterSoundLevelProcessor(url, displayName); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "rtsp").
				Context("source_url", url).
				Build())
			log.Printf("❌ Failed to register sound level processor for RTSP source %s: %v", displayName, err)
		} else {
			successCount++
			log.Printf("🔊 Registered sound level processor for RTSP source: %s", displayName)
		}
	}

	// Log summary if there were partial failures
	if len(errs) > 0 && successCount > 0 {
		log.Printf("⚠️ Registered %d of %d sound level processors successfully", successCount, totalSources)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// unregisterAllSoundLevelProcessors unregisters all sound level processors
func unregisterAllSoundLevelProcessors(settings *conf.Settings) {
	// Unregister malgo source
	if settings.Realtime.Audio.Source != "" {
		myaudio.UnregisterSoundLevelProcessor("malgo")
		log.Printf("🔇 Unregistered sound level processor for audio device: %s", settings.Realtime.Audio.Source)
	}

	// Unregister all RTSP sources
	for _, url := range settings.Realtime.RTSP.URLs {
		myaudio.UnregisterSoundLevelProcessor(url)
		log.Printf("🔇 Unregistered sound level processor for RTSP source: %s", conf.SanitizeRTSPUrl(url))
	}
}
