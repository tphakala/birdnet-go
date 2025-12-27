package analysis

import (
	"context"
	"sync"
	"time"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// getSoundLevelMetrics is a helper function to safely retrieve the SoundLevel metrics object
func getSoundLevelMetrics(apiController *apiv2.Controller) *metrics.SoundLevelMetrics {
	if apiController == nil || apiController.Processor == nil ||
		apiController.Processor.Metrics == nil || apiController.Processor.Metrics.SoundLevel == nil {
		return nil
	}
	return apiController.Processor.Metrics.SoundLevel
}

// startSoundLevelSSEPublisher starts a goroutine to consume sound level data and publish via SSE
func startSoundLevelSSEPublisher(wg *sync.WaitGroup, ctx context.Context, apiController *apiv2.Controller, soundLevelChan <-chan myaudio.SoundLevelData) {
	if apiController == nil {
		GetLogger().Warn("SSE API controller not available, sound level SSE publishing disabled")
		return
	}

	wg.Go(func() {
		GetLogger().Info("Started sound level SSE publisher")

		for {
			select {
			case <-ctx.Done():
				GetLogger().Info("Stopping sound level SSE publisher")
				return
			case soundData := <-soundLevelChan:
				// Sanitize sound level data before SSE publishing
				sanitizedData := sanitizeSoundLevelData(soundData)
				// Publish sound level data via SSE
				if err := apiController.BroadcastSoundLevel(&sanitizedData); err != nil {
					// Record error metric
					if soundLevelMetrics := getSoundLevelMetrics(apiController); soundLevelMetrics != nil {
						soundLevelMetrics.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "sse", "broadcast_error")
						soundLevelMetrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "error")
					}
					// Only log errors occasionally to avoid spam
					if time.Now().Unix()%60 == 0 { // Log once per minute at most
						GetLogger().Warn("Error broadcasting sound level data via SSE",
							logger.Error(err),
							logger.String("source", soundData.Source),
							logger.String("name", soundData.Name))
					}
				} else {
					// Record success metric
					if soundLevelMetrics := getSoundLevelMetrics(apiController); soundLevelMetrics != nil {
						soundLevelMetrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "success")
					}
				}
			}
		}
	})
}
