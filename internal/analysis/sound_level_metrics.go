package analysis

import (
	"log"
	"math"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

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
	}

	// Record processing duration
	processingDuration := time.Since(startTime).Seconds()
	metrics.SoundLevel.RecordSoundLevelProcessingDuration(soundData.Source, soundData.Name, "update_metrics", processingDuration)
}
