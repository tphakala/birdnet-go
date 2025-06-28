package analysis

import (
	"log"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Package-level logger for sound level metrics
var (
	metricsLogger *slog.Logger
	loggerOnce    sync.Once
)

// getMetricsLogger returns the metrics logger, initializing it if necessary
func getMetricsLogger() *slog.Logger {
	loggerOnce.Do(func() {
		// Initialize the logging system if not already done
		logging.Init()
		metricsLogger = logging.ForService("sound-level-metrics")
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
		metrics.SoundLevel.UpdateOctaveBandLevel(
			soundData.Source,
			soundData.Name,
			bandKey,
			bandData.Min,
			bandData.Max,
			bandData.Mean,
		)

		// Log detailed band metrics if debug is enabled
		if conf.Setting().Realtime.Audio.SoundLevel.Debug {
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
