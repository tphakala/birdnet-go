package analysis

import (
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// startSoundLevelSSEPublisher starts a goroutine to consume sound level data and publish via SSE
func startSoundLevelSSEPublisher(wg *sync.WaitGroup, quitChan chan struct{}, apiController *api.Controller) {
	if apiController == nil {
		log.Println("⚠️ SSE API controller not available, sound level SSE publishing disabled")
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		log.Println("📡 Started sound level SSE publisher")

		for {
			select {
			case <-quitChan:
				log.Println("🔌 Stopping sound level SSE publisher")
				return
			case soundData := <-soundLevelChan:
				// Publish sound level data via SSE
				if err := apiController.BroadcastSoundLevel(&soundData); err != nil {
					// Record error metric
					if apiController.Processor != nil && apiController.Processor.Metrics != nil && apiController.Processor.Metrics.SoundLevel != nil {
						apiController.Processor.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "sse", "broadcast_error")
						apiController.Processor.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "error")
					}
					// Only log errors occasionally to avoid spam
					if time.Now().Unix()%60 == 0 { // Log once per minute at most
						log.Printf("⚠️ Error broadcasting sound level data via SSE: %v", err)
					}
				} else {
					// Record success metric
					if apiController.Processor != nil && apiController.Processor.Metrics != nil && apiController.Processor.Metrics.SoundLevel != nil {
						apiController.Processor.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "success")
					}
				}
			}
		}
	}()
}
