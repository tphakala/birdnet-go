package analysis

import (
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// getMetricsLogger returns the metrics logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getMetricsLogger() logger.Logger {
	return logger.Global().Module("analysis").Module("soundlevel").Module("metrics")
}

// startSoundLevelMetricsPublisher starts a goroutine to consume sound level data and update Prometheus metrics
func startSoundLevelMetricsPublisher(wg *sync.WaitGroup, quitChan chan struct{}, metrics *observability.Metrics) {
	lg := getMetricsLogger()
	if metrics == nil || metrics.SoundLevel == nil {
		lg.Warn("sound level metrics not available, metrics publishing disabled")
		return
	}

	wg.Go(func() {
		lg.Info("started sound level metrics publisher")

		for {
			select {
			case <-quitChan:
				lg.Info("stopping sound level metrics publisher")
				return
			case soundData := <-soundLevelChan:
				// Update metrics for each octave band
				updateSoundLevelMetrics(soundData, metrics)
			}
		}
	})
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
		lg := getMetricsLogger()
		lg.Debug("updating sound level metrics",
			logger.String("source", soundData.Source),
			logger.String("name", soundData.Name),
			logger.Time("timestamp", soundData.Timestamp),
			logger.Int("duration", soundData.Duration),
			logger.Int("bands_count", len(soundData.OctaveBands)))
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
			lg := getMetricsLogger()
			lg.Debug("updated octave band metrics",
				logger.String("source", soundData.Source),
				logger.String("band", bandKey),
				logger.Float64("min_db", bandData.Min),
				logger.Float64("max_db", bandData.Max),
				logger.Float64("mean_db", bandData.Mean),
				logger.Int("samples", bandData.SampleCount))
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
			lg := getMetricsLogger()
			lg.Debug("calculated overall sound level",
				logger.String("source", soundData.Source),
				logger.String("name", soundData.Name),
				logger.Float64("overall_level_db", overallLevel),
				logger.Int("bands_averaged", len(soundData.OctaveBands)))
		}
	}

	// Record processing duration
	processingDuration := time.Since(startTime).Seconds()
	metrics.SoundLevel.RecordSoundLevelProcessingDuration(soundData.Source, soundData.Name, "update_metrics", processingDuration)

	// Log processing duration if debug is enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Debug {
		lg := getMetricsLogger()
		lg.Debug("sound level metrics update complete",
			logger.String("source", soundData.Source),
			logger.String("name", soundData.Name),
			logger.Float64("processing_duration_seconds", processingDuration))
	}
}
