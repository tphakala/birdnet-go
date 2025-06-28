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
// to ensure JSON marshaling succeeds. This prevents errors when publishing to MQTT, SSE, or other
// systems that require valid JSON.
func sanitizeSoundLevelData(data myaudio.SoundLevelData) myaudio.SoundLevelData {
	// Create a copy to avoid modifying the original
	sanitized := myaudio.SoundLevelData{
		Timestamp:   data.Timestamp,
		Source:      data.Source,
		Name:        data.Name,
		Duration:    data.Duration,
		OctaveBands: make(map[string]myaudio.OctaveBandData),
	}

	// Sanitize each octave band
	for key, band := range data.OctaveBands {
		sanitizedBand := myaudio.OctaveBandData{
			CenterFreq:  band.CenterFreq,
			Min:         sanitizeFloat64(band.Min, -200.0),  // Use -200 dB as minimum (effectively silence)
			Max:         sanitizeFloat64(band.Max, -200.0),  // Use -200 dB as maximum fallback
			Mean:        sanitizeFloat64(band.Mean, -200.0), // Use -200 dB as mean fallback
			SampleCount: band.SampleCount,
		}
		sanitized.OctaveBands[key] = sanitizedBand

		// Log sanitization details if debug is enabled
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			if band.Min != sanitizedBand.Min || band.Max != sanitizedBand.Max || band.Mean != sanitizedBand.Mean {
				if logger := getSoundLevelLogger(); logger != nil {
					logger.Debug("sanitized non-finite values in octave band",
						"band", key,
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
	return value
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

	// Create MQTT topic for sound level data
	topic := fmt.Sprintf("%s/soundlevel", strings.TrimSuffix(settings.Realtime.MQTT.Topic, "/"))

	// Sanitize sound level data before JSON marshaling
	sanitizedData := sanitizeSoundLevelData(soundData)

	// Marshal sound level data to JSON
	jsonData, err := json.Marshal(sanitizedData)
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
	if settings.Realtime.Audio.SoundLevel.Debug {
		if logger := getSoundLevelLogger(); logger != nil {
			logger.Debug("published sound level data to MQTT",
				"topic", topic,
				"source", soundData.Source,
				"name", soundData.Name,
				"json_size", len(jsonData),
				"octave_bands", len(soundData.OctaveBands))

			// Log each octave band's values
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
	if err := apiController.BroadcastSoundLevel(&soundData); err != nil {
		// Record error metric
		if metrics := getSoundLevelMetricsFromAPI(apiController); metrics != nil {
			metrics.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "sse", "broadcast_error")
			metrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "error")
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
	if metrics := getSoundLevelMetricsFromAPI(apiController); metrics != nil {
		metrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "success")
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
func startSoundLevelMetricsPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, metrics *observability.Metrics, soundLevelChan chan myaudio.SoundLevelData) {
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
				if metrics != nil && metrics.SoundLevel != nil {
					updateSoundLevelMetrics(soundData, metrics)
				}
			}
		}
	}()
}

// registerSoundLevelProcessorsForActiveSources registers sound level processors for all active audio sources
func registerSoundLevelProcessorsForActiveSources(settings *conf.Settings) error {
	var errs []error

	// Register for malgo source if active
	if settings.Realtime.Audio.Source != "" {
		if err := myaudio.RegisterSoundLevelProcessor("malgo", settings.Realtime.Audio.Source); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "malgo").
				Context("source_name", settings.Realtime.Audio.Source).
				Build())
		} else {
			log.Printf("🔊 Registered sound level processor for audio device: %s", settings.Realtime.Audio.Source)
		}
	}

	// Register for each RTSP source
	for _, url := range settings.Realtime.RTSP.URLs {
		displayName := conf.SanitizeRTSPUrl(url)
		if err := myaudio.RegisterSoundLevelProcessor(url, displayName); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "rtsp").
				Context("source_url", url).
				Build())
		} else {
			log.Printf("🔊 Registered sound level processor for RTSP source: %s", displayName)
		}
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
