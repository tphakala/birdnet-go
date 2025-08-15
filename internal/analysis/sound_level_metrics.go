package analysis

import (
	"io"
	"log"
	"log/slog"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Package-level logger for sound level metrics
var (
	metricsLogger      *slog.Logger
	loggerOnce         sync.Once
	metricsLevelVar    = new(slog.LevelVar) // Dynamic level control
	metricsCloseLogger func() error
)

// getMetricsLogger returns the metrics logger, initializing it if necessary
func getMetricsLogger() *slog.Logger {
	loggerOnce.Do(func() {
		var err error
		// Define log file path relative to working directory - use same file as sound level
		logFilePath := filepath.Join("logs", "soundlevel.log")
		// Set initial level based on debug flag
		initialLevel := slog.LevelInfo
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			initialLevel = slog.LevelDebug
		}
		metricsLevelVar.Set(initialLevel)

		// Initialize the service-specific file logger
		metricsLogger, metricsCloseLogger, err = logging.NewFileLogger(logFilePath, "sound-level-metrics", metricsLevelVar)
		if err != nil {
			// Fallback: Log error to standard log and use stdout logger
			log.Printf("WARNING: Failed to initialize sound level metrics file logger at %s: %v. Using console logging.", logFilePath, err)
			// Fallback to console logger
			logging.Init()
			fbHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: metricsLevelVar})
			metricsLogger = slog.New(fbHandler).With("service", "sound-level-metrics")
			metricsCloseLogger = func() error { return nil } // No-op closer
		}
	})
	return metricsLogger
}

// startSoundLevelMetricsPublisher starts a goroutine to consume sound level data and update Prometheus metrics
func startSoundLevelMetricsPublisher(wg *sync.WaitGroup, quitChan chan struct{}, metrics *observability.Metrics) {
	if metrics == nil || metrics.SoundLevel == nil {
		log.Println("⚠️ Sound level metrics not available, metrics publishing disabled")
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		log.Println("📊 Started sound level metrics publisher")

		for {
			select {
			case <-quitChan:
				log.Println("🔌 Stopping sound level metrics publisher")
				return
			case soundData := <-soundLevelChan:
				// Update metrics for each octave band
				updateSoundLevelMetrics(soundData, metrics)
			}
		}
	}()
}

// updateSoundLevelMetrics updates Prometheus metrics with sound level data
func updateSoundLevelMetrics(soundData myaudio.SoundLevelData, metrics *observability.Metrics) {
	if metrics.SoundLevel == nil {
		return
	}

	startTime := time.Now()

	// Record the measurement duration
	metrics.SoundLevel.RecordSoundLevelDuration(soundData.Source, soundData.Name, float64(soundData.Duration))

	// Log metrics update if debug is enabled
	// This is logged at interval rate, not realtime
	if conf.Setting().Realtime.Audio.SoundLevel.Debug {
		if logger := getMetricsLogger(); logger != nil {
			logger.Debug("updating sound level metrics",
				"source", soundData.Source,
				"name", soundData.Name,
				"timestamp", soundData.Timestamp,
				"duration", soundData.Duration,
				"bands_count", len(soundData.OctaveBands))
		}
	}

	// Update metrics for each octave band
	for bandKey, bandData := range soundData.OctaveBands {
		// Round values to 2 decimal places for cleaner metrics
		metrics.SoundLevel.UpdateOctaveBandLevel(
			soundData.Source,
			soundData.Name,
			bandKey,
			math.Round(bandData.Min*100)/100,
			math.Round(bandData.Max*100)/100,
			math.Round(bandData.Mean*100)/100,
		)

		// Log detailed band metrics if debug is enabled and realtime logging is on
		if conf.Setting().Realtime.Audio.SoundLevel.Debug && conf.Setting().Realtime.Audio.SoundLevel.DebugRealtimeLogging {
			if logger := getMetricsLogger(); logger != nil {
				logger.Debug("updated octave band metrics",
					"source", soundData.Source,
					"band", bandKey,
					"min_db", bandData.Min,
					"max_db", bandData.Max,
					"mean_db", bandData.Mean,
					"samples", bandData.SampleCount)
			}
		}
	}

	// Calculate overall sound level using logarithmic averaging
	// Sound levels in dB must be converted to power, averaged, then converted back
	if len(soundData.OctaveBands) > 0 {
		var totalPower float64
		for _, bandData := range soundData.OctaveBands {
			// Convert dB to power: power = 10^(dB/10)
			power := math.Pow(10, bandData.Mean/10.0)
			totalPower += power
		}
		// Average the power values
		avgPower := totalPower / float64(len(soundData.OctaveBands))
		// Convert back to dB: dB = 10 * log10(power)
		overallLevel := 10 * math.Log10(avgPower)
		// Round to 2 decimal places
		overallLevel = math.Round(overallLevel*100) / 100
		metrics.SoundLevel.UpdateSoundLevel(soundData.Source, soundData.Name, "overall", overallLevel)

		// Log overall sound level if debug is enabled
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
			if logger := getMetricsLogger(); logger != nil {
				logger.Debug("calculated overall sound level",
					"source", soundData.Source,
					"name", soundData.Name,
					"overall_level_db", overallLevel,
					"bands_averaged", len(soundData.OctaveBands))
			}
		}
	}

	// Record processing duration
	processingDuration := time.Since(startTime).Seconds()
	metrics.SoundLevel.RecordSoundLevelProcessingDuration(soundData.Source, soundData.Name, "update_metrics", processingDuration)

	// Log processing duration if debug is enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Debug {
		if logger := getMetricsLogger(); logger != nil {
			logger.Debug("sound level metrics update complete",
				"source", soundData.Source,
				"name", soundData.Name,
				"processing_duration_seconds", processingDuration)
		}
	}
}
