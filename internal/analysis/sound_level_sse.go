package analysis

import (
	"context"
	"log"
	"sync"
	"time"

	api "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// getSoundLevelMetrics is a helper function to safely retrieve the SoundLevel metrics object
func getSoundLevelMetrics(apiController *api.Controller) *metrics.SoundLevelMetrics {
	if apiController == nil || apiController.Processor == nil ||
		apiController.Processor.Metrics == nil || apiController.Processor.Metrics.SoundLevel == nil {
		return nil
	}
	return apiController.Processor.Metrics.SoundLevel
}

// startSoundLevelSSEPublisher starts a goroutine to consume sound level data and publish via SSE
func startSoundLevelSSEPublisher(wg *sync.WaitGroup, ctx context.Context, apiController *api.Controller, soundLevelChan <-chan myaudio.SoundLevelData) {
	if apiController == nil {
		log.Println("⚠️ SSE API controller not available, sound level SSE publishing disabled")
		return
	}

	wg.Go(func() {

		log.Println("📡 Started sound level SSE publisher")

		for {
			select {
			case <-ctx.Done():
				log.Println("🔌 Stopping sound level SSE publisher")
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
						log.Printf("⚠️ Error broadcasting sound level data via SSE: %v", err)
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
