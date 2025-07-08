package myaudio

import (
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Global FFmpeg manager instance
var (
	globalManager *FFmpegManager
	managerOnce   sync.Once
	managerMutex  sync.RWMutex
)

// getGlobalManager returns the global FFmpeg manager instance
func getGlobalManager() *FFmpegManager {
	managerOnce.Do(func() {
		globalManager = NewFFmpegManager()
		// Start monitoring with 30-second interval
		globalManager.StartMonitoring(30 * time.Second)
	})
	return globalManager
}

// CaptureAudioRTSP provides backward compatibility with the old API
// This function now delegates to the new simplified FFmpeg manager
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// Register channels for backward compatibility
	RegisterStreamChannels(url, restartChan, unifiedAudioChan)
	defer UnregisterStreamChannels(url)

	// Initialize sound level processor if enabled
	settings := conf.Setting()
	displayName := privacy.SanitizeRTSPUrl(url)
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := RegisterSoundLevelProcessor(url, displayName); err != nil {
			log.Printf("❌ Error initializing sound level processor for %s: %v", url, err)
		}
		defer UnregisterSoundLevelProcessor(url)
	}

	// Check FFmpeg availability
	if conf.GetFfmpegBinaryName() == "" {
		log.Printf("❌ FFmpeg is not available, cannot capture audio from %s", url)
		return
	}

	// Get the global manager
	manager := getGlobalManager()

	// Start the stream
	if err := manager.StartStream(url, transport, unifiedAudioChan); err != nil {
		log.Printf("❌ Failed to start stream for %s: %v", url, err)
		return
	}

	// Handle restart signals in a separate goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-quitChan:
				// Stop the stream
				if err := manager.StopStream(url); err != nil {
					log.Printf("⚠️ Error stopping stream %s: %v", url, err)
				}
				return
			case <-restartChan:
				// Restart the stream
				if err := manager.RestartStream(url); err != nil {
					log.Printf("⚠️ Error restarting stream %s: %v", url, err)
				}
			}
		}
	}()
}

// SyncRTSPStreamsWithConfig synchronizes running RTSP streams with configuration
// This is called when configuration changes to start/stop streams as needed
func SyncRTSPStreamsWithConfig(audioChan chan UnifiedAudioData) error {
	manager := getGlobalManager()
	return manager.SyncWithConfig(audioChan)
}

// GetRTSPStreamHealth returns health information for all RTSP streams
func GetRTSPStreamHealth() map[string]StreamHealth {
	manager := getGlobalManager()
	return manager.HealthCheck()
}

// ShutdownFFmpegManager gracefully shuts down the FFmpeg manager
func ShutdownFFmpegManager() {
	managerMutex.Lock()
	defer managerMutex.Unlock()

	if globalManager != nil {
		globalManager.Shutdown()
		globalManager = nil
		managerOnce = sync.Once{} // Reset the once to allow re-initialization
	}
}

