package analysis

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// ControlMonitor handles control signals for realtime analysis mode
type ControlMonitor struct {
	wg               *sync.WaitGroup
	controlChan      chan string
	quitChan         chan struct{}
	restartChan      chan struct{}
	notificationChan chan handlers.Notification
	bufferManager    *BufferManager
	proc             *processor.Processor
	audioLevelChan   chan myaudio.AudioLevelData
	soundLevelChan   chan myaudio.SoundLevelData
	bn               *birdnet.BirdNET

	// Track unified audio channel and its done channel to prevent goroutine leaks
	unifiedAudioChan     chan myaudio.UnifiedAudioData
	unifiedAudioDoneChan chan struct{}
	unifiedAudioMutex    sync.Mutex

	// Track sound level publisher goroutines
	soundLevelPublishersWg    *sync.WaitGroup
	soundLevelPublishersDone  chan struct{}
	soundLevelPublishersMutex sync.Mutex
}

// NewControlMonitor creates a new ControlMonitor instance
func NewControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor, audioLevelChan chan myaudio.AudioLevelData, soundLevelChan chan myaudio.SoundLevelData) *ControlMonitor {
	return &ControlMonitor{
		wg:                     wg,
		controlChan:            controlChan,
		quitChan:               quitChan,
		restartChan:            restartChan,
		notificationChan:       notificationChan,
		bufferManager:          bufferManager,
		audioLevelChan:         audioLevelChan,
		soundLevelChan:         soundLevelChan,
		proc:                   proc,
		bn:                     proc.Bn,
		soundLevelPublishersWg: &sync.WaitGroup{},
	}
}

// Start begins monitoring control signals
func (cm *ControlMonitor) Start() {
	go cm.monitor()
}

// monitor listens for control signals and handles them
func (cm *ControlMonitor) monitor() {
	for {
		select {
		case signal := <-cm.controlChan:
			cm.handleControlSignal(signal)
		case <-cm.quitChan:
			return
		}
	}
}

// handleControlSignal processes different control signals
func (cm *ControlMonitor) handleControlSignal(signal string) {
	switch signal {
	case "rebuild_range_filter":
		cm.handleRebuildRangeFilter()
	case "reload_birdnet":
		cm.handleReloadBirdnet()
	case "reconfigure_mqtt":
		cm.handleReconfigureMQTT()
	case "reconfigure_rtsp_sources":
		cm.handleReconfigureRTSP()
	case "reconfigure_birdweather":
		cm.handleReconfigureBirdWeather()
	case "update_detection_intervals":
		cm.handleUpdateDetectionIntervals()
	case "reconfigure_sound_level":
		cm.handleReconfigureSoundLevel()
	default:
		log.Printf("Received unknown control signal: %v", signal)
	}
}

// handleRebuildRangeFilter rebuilds the range filter
func (cm *ControlMonitor) handleRebuildRangeFilter() {
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		log.Printf("\033[31m❌ Error handling range filter rebuild: %v\033[0m", err)
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		log.Printf("\033[32m🔄 Range filter rebuilt successfully\033[0m")
		cm.notifySuccess("Range filter rebuilt successfully")
	}
}

// handleReloadBirdnet reloads the BirdNET model
func (cm *ControlMonitor) handleReloadBirdnet() {
	if err := cm.bn.ReloadModel(); err != nil {
		log.Printf("\033[31m❌ Error reloading BirdNET model: %v\033[0m", err)
		cm.notifyError("Failed to reload BirdNET model", err)
		return
	}

	log.Printf("\033[32m✅ BirdNET model reloaded successfully\033[0m")
	cm.notifySuccess("BirdNET model reloaded successfully")

	// Rebuild range filter after model reload
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		log.Printf("\033[31m❌ Error rebuilding range filter after model reload: %v\033[0m", err)
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		log.Printf("\033[32m✅ Range filter rebuilt successfully\033[0m")
		cm.notifySuccess("Range filter rebuilt successfully")
	}
}

// handleReconfigureMQTT reconfigures the MQTT connection
func (cm *ControlMonitor) handleReconfigureMQTT() {
	log.Printf("\033[32m🔄 Reconfiguring MQTT connection...\033[0m")
	settings := conf.Setting()

	if cm.proc == nil {
		log.Printf("\033[31m❌ Error: Processor not available\033[0m")
		cm.notifyError("Failed to reconfigure MQTT", fmt.Errorf("processor not available"))
		return
	}

	// First, safely disconnect any existing client
	cm.proc.DisconnectMQTTClient()

	// If MQTT is enabled, initialize and connect
	if settings.Realtime.MQTT.Enabled {
		var err error
		newClient, err := mqtt.NewClient(settings, cm.proc.Metrics)
		if err != nil {
			log.Printf("\033[31m❌ Error creating MQTT client: %v\033[0m", err)
			cm.notifyError("Failed to create MQTT client", err)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := newClient.Connect(ctx); err != nil {
			cancel()
			log.Printf("\033[31m❌ Error connecting to MQTT broker: %v\033[0m", err)
			cm.notifyError("Failed to connect to MQTT broker", err)
			return
		}
		cancel()

		// Safely set the new client
		cm.proc.SetMQTTClient(newClient)

		log.Printf("\033[32m✅ MQTT connection configured successfully\033[0m")
		cm.notifySuccess("MQTT connection configured successfully")
	} else {
		log.Printf("\033[32m✅ MQTT connection disabled\033[0m")
		cm.notifySuccess("MQTT connection disabled")
	}
}

// handleReconfigureRTSP reconfigures RTSP sources
func (cm *ControlMonitor) handleReconfigureRTSP() {
	log.Printf("\033[32m🔄 Reconfiguring RTSP sources...\033[0m")
	settings := conf.Setting()

	// Prepare the list of active sources
	var sources []string
	if len(settings.Realtime.RTSP.URLs) > 0 {
		sources = append(sources, settings.Realtime.RTSP.URLs...)
	}
	if settings.Realtime.Audio.Source != "" {
		sources = append(sources, "malgo")
	}

	// Update the analysis buffer monitors
	cm.bufferManager.UpdateMonitors(sources)

	// Reconfigure RTSP streams with proper goroutine cleanup
	cm.unifiedAudioMutex.Lock()

	// Close previous goroutine if it exists
	if cm.unifiedAudioDoneChan != nil {
		close(cm.unifiedAudioDoneChan)
		// Give the goroutine time to exit
		time.Sleep(100 * time.Millisecond)
	}

	// Close previous channel if it exists
	if cm.unifiedAudioChan != nil {
		close(cm.unifiedAudioChan)
	}

	// Create new channels
	cm.unifiedAudioChan = make(chan myaudio.UnifiedAudioData, 100)
	cm.unifiedAudioDoneChan = make(chan struct{})

	// Store references for cleanup
	doneChan := cm.unifiedAudioDoneChan
	unifiedChan := cm.unifiedAudioChan

	cm.unifiedAudioMutex.Unlock()

	go func() {
		// Convert unified audio data back to separate channels for existing handlers
		for {
			select {
			case <-doneChan:
				// Exit goroutine when done channel is closed
				return
			case unifiedData, ok := <-unifiedChan:
				if !ok {
					// Channel closed, exit goroutine
					return
				}

				// Send audio level data to existing audio level channel
				select {
				case cm.audioLevelChan <- unifiedData.AudioLevel:
				default:
					// Channel full, drop data
				}

				// Send sound level data to existing sound level channel if present
				if unifiedData.SoundLevel != nil {
					select {
					case cm.soundLevelChan <- *unifiedData.SoundLevel:
					default:
						// Channel full, drop data
					}
				}
			}
		}
	}()

	myaudio.ReconfigureRTSPStreams(settings, cm.wg, cm.quitChan, cm.restartChan, cm.unifiedAudioChan)

	log.Printf("\033[32m✅ RTSP sources reconfigured successfully\033[0m")
	cm.notifySuccess("Audio capture reconfigured successfully")
}

// handleReconfigureBirdWeather reconfigures the BirdWeather integration
func (cm *ControlMonitor) handleReconfigureBirdWeather() {
	log.Printf("\033[32m🔄 Reconfiguring BirdWeather integration...\033[0m")
	settings := conf.Setting()

	if cm.proc == nil {
		log.Printf("\033[31m❌ Error: Processor not available\033[0m")
		cm.notifyError("Failed to reconfigure BirdWeather", fmt.Errorf("processor not available"))
		return
	}

	// First, safely disconnect any existing client
	cm.proc.DisconnectBwClient()

	// Create new BirdWeather client with updated settings
	if settings.Realtime.Birdweather.Enabled {
		bwClient, err := birdweather.New(settings)
		if err != nil {
			log.Printf("\033[31m❌ Error creating BirdWeather client: %v\033[0m", err)
			cm.notifyError("Failed to create BirdWeather client", err)
			return
		}

		// Update the processor's BirdWeather client using the thread-safe setter
		cm.proc.SetBwClient(bwClient)
		log.Printf("\033[32m✅ BirdWeather integration configured successfully\033[0m")
		cm.notifySuccess("BirdWeather integration configured successfully")
	} else {
		// If BirdWeather is disabled, client is already set to nil by DisconnectBwClient
		log.Printf("\033[32m✅ BirdWeather integration disabled\033[0m")
		cm.notifySuccess("BirdWeather integration disabled")
	}
}

// handleUpdateDetectionIntervals updates event tracking intervals for species
func (cm *ControlMonitor) handleUpdateDetectionIntervals() {
	log.Printf("\033[32m🔄 Updating detection rate limits...\033[0m")
	settings := conf.Setting()

	if cm.proc == nil {
		log.Printf("\033[31m❌ Error: Processor not available\033[0m")
		cm.notifyError("Failed to update detection intervals", fmt.Errorf("processor not available"))
		return
	}

	// Validate global interval setting
	globalInterval := time.Duration(settings.Realtime.Interval) * time.Second
	if globalInterval <= 0 {
		log.Printf("\033[33m⚠️ Warning: Invalid global interval value (%v), using default\033[0m", globalInterval)
		globalInterval = 5 * time.Second // Fallback to a reasonable default
	}

	// Note: If EventTracker cleanup becomes necessary in the future,
	// get the current tracker here and perform cleanup before replacement

	// Create a new EventTracker with updated settings
	newTracker := processor.NewEventTrackerWithConfig(
		globalInterval,
		settings.Realtime.Species.Config,
	)

	// Clean up the old EventTracker if possible
	// Note: If cleanup becomes necessary in the future, consider adding a Close()
	// method to the EventTracker type and call it here

	// Replace the existing EventTracker with the new one
	cm.proc.SetEventTracker(newTracker)

	log.Printf("\033[32m✅ Detection rate limits updated successfully\033[0m")
	cm.notifySuccess("Detection rate limits updated successfully")
}

// notifySuccess sends a success notification
func (cm *ControlMonitor) notifySuccess(message string) {
	cm.notificationChan <- handlers.Notification{
		Message: message,
		Type:    "success",
	}
}

// notifyError sends an error notification
func (cm *ControlMonitor) notifyError(message string, err error) {
	cm.notificationChan <- handlers.Notification{
		Message: fmt.Sprintf("%s: %v", message, err),
		Type:    "error",
	}
}

// handleReconfigureSoundLevel reconfigures sound level monitoring
func (cm *ControlMonitor) handleReconfigureSoundLevel() {
	log.Printf("🔄 Reconfiguring sound level monitoring...")
	settings := conf.Setting()

	// Lock the mutex to ensure thread-safe access
	cm.soundLevelPublishersMutex.Lock()
	defer cm.soundLevelPublishersMutex.Unlock()

	// Check if we need to stop existing publishers
	if cm.soundLevelPublishersDone != nil {
		// Signal existing publishers to stop
		close(cm.soundLevelPublishersDone)
		// Wait for all publishers to finish
		cm.soundLevelPublishersWg.Wait()
		cm.soundLevelPublishersDone = nil
	}

	// If sound level monitoring is enabled, start new publishers
	if settings.Realtime.Audio.SoundLevel.Enabled {
		// Create a new done channel
		cm.soundLevelPublishersDone = make(chan struct{})

		// Start sound level publishers
		startSoundLevelPublishers(cm.soundLevelPublishersWg, cm.soundLevelPublishersDone, cm.proc, cm.soundLevelChan)

		log.Printf("✅ Sound level monitoring enabled")
		cm.notifySuccess("Sound level monitoring enabled")
	} else {
		log.Printf("✅ Sound level monitoring disabled")
		cm.notifySuccess("Sound level monitoring disabled")
	}
}
