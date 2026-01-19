package analysis

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/birdweather"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// ControlMonitor handles control signals for realtime analysis mode
type ControlMonitor struct {
	wg             *sync.WaitGroup
	controlChan    chan string
	quitChan       chan struct{}
	restartChan    chan struct{}
	bufferManager  *BufferManager
	proc           *processor.Processor
	audioLevelChan chan myaudio.AudioLevelData
	soundLevelChan chan myaudio.SoundLevelData
	bn             *birdnet.BirdNET
	apiController  *apiv2.Controller

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
func NewControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, bufferManager *BufferManager, proc *processor.Processor, audioLevelChan chan myaudio.AudioLevelData, soundLevelChan chan myaudio.SoundLevelData, apiController *apiv2.Controller, metrics *observability.Metrics) *ControlMonitor {
	cm := &ControlMonitor{
		wg:             wg,
		controlChan:    controlChan,
		quitChan:       quitChan,
		restartChan:    restartChan,
		bufferManager:  bufferManager,
		audioLevelChan: audioLevelChan,
		soundLevelChan: soundLevelChan,
		proc:           proc,
		bn:             proc.Bn,
		apiController:  apiController,
		metrics:        metrics,
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
			cm.soundLevelManager = NewSoundLevelManager(cm.soundLevelChan, cm.proc, cm.apiController, cm.metrics)
		}

		// Start sound level monitoring
		if err := cm.soundLevelManager.Start(); err != nil {
			GetLogger().Warn("Failed to start sound level monitoring", logger.Error(err))
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
		GetLogger().Warn("Metrics not initialized, skipping telemetry endpoint initialization")
		return
	}

	settings := conf.Setting()
	if settings.Realtime.Telemetry.Enabled {
		cm.telemetryEndpointMutex.Lock()
		defer cm.telemetryEndpointMutex.Unlock()

		// Validate listen address format
		if err := cm.validateListenAddress(settings.Realtime.Telemetry.Listen); err != nil {
			GetLogger().Warn("Invalid telemetry listen address", logger.Error(err))
			return
		}

		// Create quit channel
		cm.telemetryQuitChan = make(chan struct{})

		// Initialize endpoint
		endpoint, err := observability.NewEndpoint(settings, cm.metrics)
		if err != nil {
			GetLogger().Error("Failed to initialize telemetry endpoint", logger.Error(err))
			return
		}

		// Start the endpoint
		endpoint.Start(&cm.telemetryWg, cm.telemetryQuitChan)
		cm.telemetryEndpoint = endpoint

		GetLogger().Info("Telemetry endpoint started", logger.String("address", settings.Realtime.Telemetry.Listen))
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
		cm.handleReconfigureStreams()
	case "reconfigure_birdweather":
		cm.handleReconfigureBirdWeather()
	case "update_detection_intervals":
		cm.handleUpdateDetectionIntervals()
	case "reconfigure_sound_level":
		cm.handleReconfigureSoundLevel()
	case "reconfigure_telemetry":
		cm.handleReconfigureTelemetry()
	case "reconfigure_species_tracking":
		cm.handleReconfigureSpeciesTracking()
	default:
		GetLogger().Warn("Received unknown control signal", logger.String("signal", signal))
	}
}

// handleRebuildRangeFilter rebuilds the range filter
func (cm *ControlMonitor) handleRebuildRangeFilter() {
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		GetLogger().Error("Failed to rebuild range filter", logger.Error(err))
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		GetLogger().Info("Range filter rebuilt successfully")
		cm.notifySuccess("Range filter rebuilt successfully")
	}

	// Perform log deduplicator cleanup when range filter is rebuilt
	// This coupling is for practicality - we wanted to avoid creating new goroutines
	// and the range filter rebuild happens periodically, making it a convenient hook
	// for maintenance tasks. Future maintainers: this is just opportunistic cleanup,
	// not a functional requirement of range filter rebuilding.
	if cm.proc != nil {
		// Clean entries older than 1 hour
		cm.proc.CleanupLogDeduplicator(time.Hour)
	}
}

// handleReloadBirdnet reloads the BirdNET model
func (cm *ControlMonitor) handleReloadBirdnet() {
	if err := cm.bn.ReloadModel(); err != nil {
		GetLogger().Error("Failed to reload BirdNET model", logger.Error(err))
		cm.notifyError("Failed to reload BirdNET model", err)
		return
	}

	GetLogger().Info("BirdNET model reloaded successfully")
	cm.notifySuccess("BirdNET model reloaded successfully")

	// Rebuild range filter after model reload
	if err := birdnet.BuildRangeFilter(cm.bn); err != nil {
		GetLogger().Error("Failed to rebuild range filter after model reload", logger.Error(err))
		cm.notifyError("Failed to rebuild range filter", err)
	} else {
		GetLogger().Info("Range filter rebuilt successfully")
		cm.notifySuccess("Range filter rebuilt successfully")
	}
}

// handleReconfigureMQTT reconfigures the MQTT connection
func (cm *ControlMonitor) handleReconfigureMQTT() {
	GetLogger().Info("Reconfiguring MQTT connection")
	settings := conf.Setting()

	if cm.proc == nil {
		GetLogger().Error("Processor not available for MQTT reconfiguration")
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
			GetLogger().Error("Failed to create MQTT client", logger.Error(err))
			cm.notifyError("Failed to create MQTT client", err)
			return
		}

		// Register Home Assistant discovery handler before connecting
		// so the OnConnect handler fires on the initial connection
		cm.proc.RegisterHomeAssistantDiscovery(newClient, settings)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := newClient.Connect(ctx); err != nil {
			cancel()
			GetLogger().Error("Failed to connect to MQTT broker", logger.Error(err))
			cm.notifyError("Failed to connect to MQTT broker", err)
			return
		}
		cancel()

		// Safely set the new client
		cm.proc.SetMQTTClient(newClient)

		GetLogger().Info("MQTT connection configured successfully")
		cm.notifySuccess("MQTT connection configured successfully")
	} else {
		GetLogger().Info("MQTT connection disabled")
		cm.notifySuccess("MQTT connection disabled")
	}
}

// handleReconfigureStreams reconfigures audio streams
func (cm *ControlMonitor) handleReconfigureStreams() {
	GetLogger().Info("Reconfiguring audio streams")
	settings := conf.Setting()

	// Prepare the list of active sources (using source IDs, not raw URLs)
	var sources []string
	if len(settings.Realtime.RTSP.Streams) > 0 {
		registry := myaudio.GetRegistry()
		if registry != nil {
			for _, stream := range settings.Realtime.RTSP.Streams {
				// Use GetOrCreateSourceWithName to ensure DisplayName is updated when stream name changes
				if streamSource := registry.GetOrCreateSourceWithName(stream.URL, myaudio.StreamTypeToSourceType(stream.Type), stream.Name); streamSource != nil {
					sources = append(sources, streamSource.ID)
				} else {
					GetLogger().Warn("Failed to get stream source ID from registry during reconfiguration",
						logger.String("stream_name", stream.Name))
				}
			}
		} else {
			GetLogger().Warn("Registry not available during stream reconfiguration, skipping stream sources")
		}
	}
	if settings.Realtime.Audio.Source != "" {
		// Get the audio source from registry instead of hardcoded "malgo"
		if registry := myaudio.GetRegistry(); registry != nil {
			if audioSource := registry.GetOrCreateSource(settings.Realtime.Audio.Source, myaudio.SourceTypeAudioCard); audioSource != nil {
				sources = append(sources, audioSource.ID)
			} else {
				GetLogger().Warn("Failed to get audio source from registry during stream reconfiguration")
			}
		} else {
			GetLogger().Warn("Registry not available during stream reconfiguration, skipping audio source")
		}
	}

	// Update the analysis buffer monitors
	if err := cm.bufferManager.UpdateMonitors(sources); err != nil {
		GetLogger().Warn("Buffer monitor update completed with errors", logger.Error(err))
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

	myaudio.ReconfigureStreams(settings, cm.wg, cm.quitChan, cm.restartChan, cm.unifiedAudioChan)

	GetLogger().Info("Audio streams reconfigured successfully")
	cm.notifySuccess("Audio capture reconfigured successfully")
}

// handleReconfigureBirdWeather reconfigures the BirdWeather integration
func (cm *ControlMonitor) handleReconfigureBirdWeather() {
	GetLogger().Info("Reconfiguring BirdWeather integration")
	settings := conf.Setting()

	if cm.proc == nil {
		GetLogger().Error("Processor not available for BirdWeather reconfiguration")
		cm.notifyError("Failed to reconfigure BirdWeather", fmt.Errorf("processor not available"))
		return
	}

	// First, safely disconnect any existing client
	cm.proc.DisconnectBwClient()

	// Create new BirdWeather client with updated settings
	if settings.Realtime.Birdweather.Enabled {
		bwClient, err := birdweather.New(settings)
		if err != nil {
			GetLogger().Error("Failed to create BirdWeather client", logger.Error(err))
			cm.notifyError("Failed to create BirdWeather client", err)
			return
		}

		// Update the processor's BirdWeather client using the thread-safe setter
		cm.proc.SetBwClient(bwClient)
		GetLogger().Info("BirdWeather integration configured successfully")
		cm.notifySuccess("BirdWeather integration configured successfully")
	} else {
		// If BirdWeather is disabled, client is already set to nil by DisconnectBwClient
		GetLogger().Info("BirdWeather integration disabled")
		cm.notifySuccess("BirdWeather integration disabled")
	}
}

// handleUpdateDetectionIntervals updates event tracking intervals for species
func (cm *ControlMonitor) handleUpdateDetectionIntervals() {
	GetLogger().Info("Updating detection rate limits")
	settings := conf.Setting()

	if cm.proc == nil {
		GetLogger().Error("Processor not available for detection interval update")
		cm.notifyError("Failed to update detection intervals", fmt.Errorf("processor not available"))
		return
	}

	// Validate global interval setting
	globalInterval := time.Duration(settings.Realtime.Interval) * time.Second
	if globalInterval <= 0 {
		GetLogger().Warn("Invalid global interval value, using default",
			logger.String("invalid_value", globalInterval.String()),
			logger.String("default_value", "5s"))
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

	GetLogger().Info("Detection rate limits updated successfully")
	cm.notifySuccess("Detection rate limits updated successfully")
}

// notifySuccess logs a success message
func (cm *ControlMonitor) notifySuccess(message string) {
	GetLogger().Info(message, logger.String("status", "success"))
}

// notifyError logs an error message
func (cm *ControlMonitor) notifyError(message string, err error) {
	GetLogger().Error(message, logger.Error(err))
}

// handleReconfigureSoundLevel reconfigures sound level monitoring
func (cm *ControlMonitor) handleReconfigureSoundLevel() {
	GetLogger().Info("Reconfiguring sound level monitoring")

	// Initialize the sound level manager if not already created
	if cm.soundLevelManager == nil {
		cm.soundLevelManager = NewSoundLevelManager(cm.soundLevelChan, cm.proc, cm.apiController, cm.metrics)
	}

	// Restart sound level monitoring with new settings
	if err := cm.soundLevelManager.Restart(); err != nil {
		GetLogger().Error("Failed to reconfigure sound level monitoring", logger.Error(err))
		cm.notifyError("Failed to reconfigure sound level monitoring", err)
		return
	}

	settings := conf.Setting()
	if settings.Realtime.Audio.SoundLevel.Enabled {
		GetLogger().Info("Sound level monitoring reconfigured",
			logger.Int("interval_seconds", settings.Realtime.Audio.SoundLevel.Interval))
		cm.notifySuccess(fmt.Sprintf("Sound level monitoring reconfigured (interval: %ds)", settings.Realtime.Audio.SoundLevel.Interval))
	} else {
		GetLogger().Info("Sound level monitoring disabled")
		cm.notifySuccess("Sound level monitoring disabled")
	}
}

// handleReconfigureTelemetry reconfigures the telemetry/metrics endpoint
func (cm *ControlMonitor) handleReconfigureTelemetry() {
	GetLogger().Info("Reconfiguring telemetry endpoint")

	// Check if metrics is available
	if cm.metrics == nil {
		GetLogger().Error("Metrics not initialized for telemetry reconfiguration")
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
		GetLogger().Info("Stopped existing telemetry endpoint")
	}

	// If telemetry is enabled, start new endpoint
	if settings.Realtime.Telemetry.Enabled {
		// Validate listen address format
		if err := cm.validateListenAddress(settings.Realtime.Telemetry.Listen); err != nil {
			GetLogger().Error("Invalid telemetry listen address", logger.Error(err))
			cm.notifyError("Invalid telemetry listen address", err)
			return
		}

		// Create quit channel for the new endpoint
		cm.telemetryQuitChan = make(chan struct{})

		// Initialize new endpoint
		endpoint, err := observability.NewEndpoint(settings, cm.metrics)
		if err != nil {
			GetLogger().Error("Failed to initialize telemetry endpoint", logger.Error(err))
			cm.notifyError("Failed to initialize telemetry endpoint", err)
			cm.telemetryQuitChan = nil // Clean up the channel on error
			return
		}

		// Start the endpoint
		endpoint.Start(&cm.telemetryWg, cm.telemetryQuitChan)
		cm.telemetryEndpoint = endpoint

		GetLogger().Info("Telemetry endpoint reconfigured", logger.String("address", settings.Realtime.Telemetry.Listen))
		cm.notifySuccess(fmt.Sprintf("Telemetry endpoint reconfigured at %s", settings.Realtime.Telemetry.Listen))
	} else {
		GetLogger().Info("Telemetry endpoint disabled")
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

// handleReconfigureSpeciesTracking reconfigures the species tracking system
func (cm *ControlMonitor) handleReconfigureSpeciesTracking() {
	GetLogger().Info("Reconfiguring species tracking")
	settings := conf.Setting()

	if cm.proc == nil {
		GetLogger().Error("Processor not available for species tracking reconfiguration")
		cm.notifyError("Failed to reconfigure species tracking", fmt.Errorf("processor not available"))
		return
	}

	// Get the datastore from the processor
	ds := cm.proc.Ds
	if ds == nil {
		GetLogger().Error("Datastore not available for species tracking reconfiguration")
		cm.notifyError("Failed to reconfigure species tracking", fmt.Errorf("datastore not available"))
		return
	}

	// Close existing tracker to prevent goroutine leaks
	// This waits for in-flight async database operations to complete
	if existingTracker := cm.proc.GetNewSpeciesTracker(); existingTracker != nil {
		if err := existingTracker.Close(); err != nil {
			GetLogger().Warn("Failed to close existing species tracker", logger.Error(err))
			// Continue anyway - we still want to reconfigure
		}
	}

	// If species tracking is disabled, set tracker to nil
	if !settings.Realtime.SpeciesTracking.Enabled {
		cm.proc.SetNewSpeciesTracker(nil)
		GetLogger().Info("Species tracking disabled")
		cm.notifySuccess("Species tracking disabled")
		return
	}

	// Validate species tracking settings
	if err := settings.Realtime.SpeciesTracking.Validate(); err != nil {
		GetLogger().Error("Invalid species tracking configuration", logger.Error(err))
		cm.notifyError("Invalid species tracking configuration", err)
		return
	}

	// Adjust seasonal tracking for hemisphere based on BirdNET latitude
	hemisphereAwareTracking := settings.Realtime.SpeciesTracking
	if hemisphereAwareTracking.SeasonalTracking.Enabled {
		hemisphereAwareTracking.SeasonalTracking = conf.GetSeasonalTrackingWithHemisphere(
			hemisphereAwareTracking.SeasonalTracking,
			settings.BirdNET.Latitude,
		)
	}

	// Create new species tracker with updated settings
	newTracker := species.NewTrackerFromSettings(ds, &hemisphereAwareTracking)

	// Initialize species tracker from database
	if err := newTracker.InitFromDatabase(); err != nil {
		GetLogger().Warn("Failed to initialize species tracker from database", logger.Error(err))
		// Continue anyway - tracker will work for new detections
	}

	// Replace the existing tracker
	cm.proc.SetNewSpeciesTracker(newTracker)

	hemisphere := conf.DetectHemisphere(settings.BirdNET.Latitude)
	GetLogger().Info("Species tracking reconfigured",
		logger.Int("window_days", settings.Realtime.SpeciesTracking.NewSpeciesWindowDays),
		logger.Int("sync_minutes", settings.Realtime.SpeciesTracking.SyncIntervalMinutes),
		logger.String("hemisphere", hemisphere))
	cm.notifySuccess("Species tracking reconfigured successfully")
}
