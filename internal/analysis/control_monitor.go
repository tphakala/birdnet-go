package analysis

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
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
	httpServer       *httpcontroller.Server

	// Track unified audio channel and its done channel to prevent goroutine leaks
	unifiedAudioChan     chan myaudio.UnifiedAudioData
	unifiedAudioDoneChan chan struct{}
	unifiedAudioMutex    sync.Mutex
	unifiedAudioWg       sync.WaitGroup

	// Sound level manager for lifecycle management
	soundLevelManager *SoundLevelManager
	
	// Track telemetry endpoint
	telemetryEndpoint      *observability.Endpoint
	telemetryEndpointMutex sync.Mutex
	telemetryQuitChan      chan struct{}
	telemetryWg            sync.WaitGroup
	metrics                *observability.Metrics
}

// NewControlMonitor creates a new ControlMonitor instance
func NewControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor, audioLevelChan chan myaudio.AudioLevelData, soundLevelChan chan myaudio.SoundLevelData, metrics *observability.Metrics) *ControlMonitor {
	cm := &ControlMonitor{
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
		metrics:                metrics,
	}
	
	// Initialize the sound level manager but don't start it yet
	// It will be started by handleReconfigureSoundLevel based on settings
	return cm
}

// Start begins monitoring control signals
func (cm *ControlMonitor) Start() {
	// Initialize telemetry endpoint if enabled
	cm.initializeTelemetryIfEnabled()
	
	// Initialize sound level monitoring if enabled
	cm.initializeSoundLevelIfEnabled()
	
	go cm.monitor()
}

// Stop stops the control monitor and cleans up resources
func (cm *ControlMonitor) Stop() {
	// Stop sound level monitoring if running
	if cm.soundLevelManager != nil {
		cm.soundLevelManager.Stop()
	}
	
	// Stop telemetry endpoint if running
	cm.telemetryEndpointMutex.Lock()
	if cm.telemetryEndpoint != nil && cm.telemetryQuitChan != nil {
		close(cm.telemetryQuitChan)
		cm.telemetryWg.Wait()
		cm.telemetryEndpoint = nil
		cm.telemetryQuitChan = nil
	}
	cm.telemetryEndpointMutex.Unlock()
}

// initializeSoundLevelIfEnabled starts sound level monitoring if it's enabled in settings
func (cm *ControlMonitor) initializeSoundLevelIfEnabled() {
	settings := conf.Setting()
	if settings.Realtime.Audio.SoundLevel.Enabled {
		// Initialize the sound level manager
		if cm.soundLevelManager == nil {
			cm.soundLevelManager = NewSoundLevelManager(cm.soundLevelChan, cm.proc, cm.httpServer, cm.metrics)
		}
		
		// Start sound level monitoring
		if err := cm.soundLevelManager.Start(); err != nil {
			log.Printf("⚠️ Warning: Failed to start sound level monitoring: %v", err)
		}
	}
}

// initializeTelemetryIfEnabled starts the telemetry endpoint if it's enabled in settings.
// Telemetry endpoint initialization is handled by control monitor to support hot reload,
// unlike other endpoints that start directly in realtime.go. This allows users to
// dynamically enable/disable metrics without restarting the application.
func (cm *ControlMonitor) initializeTelemetryIfEnabled() {
	// Check if metrics is available
	if cm.metrics == nil {
		log.Printf("⚠️ Warning: Metrics not initialized, skipping telemetry endpoint initialization")
		return
	}
	
	settings := conf.Setting()
	if settings.Realtime.Telemetry.Enabled {
		cm.telemetryEndpointMutex.Lock()
		defer cm.telemetryEndpointMutex.Unlock()
		
		// Validate listen address format
		if err := cm.validateListenAddress(settings.Realtime.Telemetry.Listen); err != nil {
			log.Printf("⚠️ Warning: Invalid telemetry listen address: %v", err)
			return
		}
		
		// Create quit channel
		cm.telemetryQuitChan = make(chan struct{})
		
		// Initialize endpoint
		endpoint, err := observability.NewEndpoint(settings, cm.metrics)
		if err != nil {
			log.Printf("Error initializing telemetry endpoint: %v", err)
			return
		}
		
		// Start the endpoint
		endpoint.Start(&cm.telemetryWg, cm.telemetryQuitChan)
		cm.telemetryEndpoint = endpoint
		
		log.Printf("📊 Telemetry endpoint started at %s", settings.Realtime.Telemetry.Listen)
	}
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
	case "reconfigure_telemetry":
		cm.handleReconfigureTelemetry()
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
	if err := cm.bufferManager.UpdateMonitors(sources); err != nil {
		log.Printf("\033[33m⚠️  Warning: Buffer monitor update completed with errors: %v\033[0m", err)
		
		// Send warning notification to UI to inform users of partial failures
		if cm.notificationChan != nil {
			notification := handlers.Notification{
				Type:    "warning", 
				Message: fmt.Sprintf("Buffer Monitor Warning: Buffer monitor update completed with errors: %v", err),
			}
			
			// Non-blocking send to avoid blocking reconfiguration
			select {
			case cm.notificationChan <- notification:
			default:
				log.Printf("Warning: Could not send buffer monitor error notification (channel full)")
			}
		}
		
		// Note: We continue execution as this is not critical for RTSP reconfiguration
	}

	// Reconfigure RTSP streams with proper goroutine cleanup
	cm.unifiedAudioMutex.Lock()

	// Close previous goroutine if it exists
	if cm.unifiedAudioDoneChan != nil {
		close(cm.unifiedAudioDoneChan)
		// Wait for the goroutine to fully exit using WaitGroup
		cm.unifiedAudioMutex.Unlock()
		cm.unifiedAudioWg.Wait()
		cm.unifiedAudioMutex.Lock()
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

	// Add to WaitGroup before starting the goroutine
	cm.unifiedAudioWg.Add(1)

	cm.unifiedAudioMutex.Unlock()

	go func() {
		defer cm.unifiedAudioWg.Done()
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
	
	// Initialize the sound level manager if not already created
	if cm.soundLevelManager == nil {
		cm.soundLevelManager = NewSoundLevelManager(cm.soundLevelChan, cm.proc, cm.httpServer, cm.metrics)
	}
	
	// Restart sound level monitoring with new settings
	if err := cm.soundLevelManager.Restart(); err != nil {
		log.Printf("❌ Error reconfiguring sound level monitoring: %v", err)
		cm.notifyError("Failed to reconfigure sound level monitoring", err)
		return
	}
	
	settings := conf.Setting()
	if settings.Realtime.Audio.SoundLevel.Enabled {
		log.Printf("✅ Sound level monitoring reconfigured (interval: %ds)", settings.Realtime.Audio.SoundLevel.Interval)
		cm.notifySuccess(fmt.Sprintf("Sound level monitoring reconfigured (interval: %ds)", settings.Realtime.Audio.SoundLevel.Interval))
	} else {
		log.Printf("✅ Sound level monitoring disabled")
		cm.notifySuccess("Sound level monitoring disabled")
	}
}

// handleReconfigureTelemetry reconfigures the telemetry/metrics endpoint
func (cm *ControlMonitor) handleReconfigureTelemetry() {
	log.Printf("🔄 Reconfiguring telemetry endpoint...")
	
	// Check if metrics is available
	if cm.metrics == nil {
		log.Printf("❌ Error: Metrics not initialized")
		cm.notifyError("Failed to reconfigure telemetry", fmt.Errorf("metrics not initialized"))
		return
	}
	
	settings := conf.Setting()

	// Lock the mutex to ensure thread-safe access
	cm.telemetryEndpointMutex.Lock()
	defer cm.telemetryEndpointMutex.Unlock()

	// Check if we need to stop existing endpoint
	if cm.telemetryEndpoint != nil && cm.telemetryQuitChan != nil {
		// Signal existing endpoint to stop
		close(cm.telemetryQuitChan)
		// Wait for it to finish
		cm.telemetryWg.Wait()
		cm.telemetryEndpoint = nil
		cm.telemetryQuitChan = nil
		log.Printf("✅ Stopped existing telemetry endpoint")
	}

	// If telemetry is enabled, start new endpoint
	if settings.Realtime.Telemetry.Enabled {
		// Validate listen address format
		if err := cm.validateListenAddress(settings.Realtime.Telemetry.Listen); err != nil {
			log.Printf("❌ Invalid telemetry listen address: %v", err)
			cm.notifyError("Invalid telemetry listen address", err)
			return
		}
		
		// Create quit channel for the new endpoint
		cm.telemetryQuitChan = make(chan struct{})
		
		// Initialize new endpoint
		endpoint, err := observability.NewEndpoint(settings, cm.metrics)
		if err != nil {
			log.Printf("❌ Error initializing telemetry endpoint: %v", err)
			cm.notifyError("Failed to initialize telemetry endpoint", err)
			cm.telemetryQuitChan = nil  // Clean up the channel on error
			return
		}

		// Start the endpoint
		endpoint.Start(&cm.telemetryWg, cm.telemetryQuitChan)
		cm.telemetryEndpoint = endpoint

		log.Printf("✅ Telemetry endpoint reconfigured at %s", settings.Realtime.Telemetry.Listen)
		cm.notifySuccess(fmt.Sprintf("Telemetry endpoint reconfigured at %s", settings.Realtime.Telemetry.Listen))
	} else {
		log.Printf("✅ Telemetry endpoint disabled")
		cm.notifySuccess("Telemetry endpoint disabled")
	}
}

// validateListenAddress checks if the listen address is in a valid format
func (cm *ControlMonitor) validateListenAddress(address string) error {
	if address == "" {
		return fmt.Errorf("listen address cannot be empty")
	}
	
	// Check if it contains a colon (for port)
	if !strings.Contains(address, ":") {
		return fmt.Errorf("listen address must include port (e.g., '0.0.0.0:8090')")
	}
	
	// Split and validate components
	parts := strings.Split(address, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid address format, expected 'host:port'")
	}
	
	// Validate port is numeric
	port := parts[1]
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid port number: %s", port)
	}
	
	return nil
}
