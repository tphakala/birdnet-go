package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/weather"
	"golang.org/x/time/rate"
)

// audioLevelChan is a channel to send audio level updates
var audioLevelChan = make(chan myaudio.AudioLevelData, 100)

// soundLevelChan is a channel to send sound level updates
var soundLevelChan = make(chan myaudio.SoundLevelData, 100)

// AudioDemuxManager manages the lifecycle of audio demultiplexing goroutines
type AudioDemuxManager struct {
	doneChan chan struct{}
	mutex    sync.Mutex
	wg       sync.WaitGroup
}

// NewAudioDemuxManager creates a new AudioDemuxManager
func NewAudioDemuxManager() *AudioDemuxManager {
	return &AudioDemuxManager{}
}

// Stop signals the current demux goroutine to stop and waits for it to exit
func (m *AudioDemuxManager) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if m.doneChan != nil {
		close(m.doneChan)
		m.wg.Wait() // Wait for goroutine to exit
		m.doneChan = nil
	}
}

// Start creates a new done channel and increments the wait group
func (m *AudioDemuxManager) Start() chan struct{} {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.doneChan = make(chan struct{})
	m.wg.Add(1)
	return m.doneChan
}

// Done should be called when the goroutine exits
func (m *AudioDemuxManager) Done() {
	m.wg.Done()
}

// Global audio demux manager instance
var audioDemuxManager = NewAudioDemuxManager()

// RealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func RealtimeAnalysis(settings *conf.Settings, notificationChan chan handlers.Notification) error {
	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		return err
	}

	// Clean up any leftover HLS streaming files from previous runs
	if err := cleanupHLSStreamingFiles(); err != nil {
		log.Printf("⚠️ Warning: Failed to clean up HLS streaming files: %v", err)
	} else {
		log.Println("🧹 Cleaned up leftover HLS streaming files")
	}

	// Initialize occurrence monitor to filter out repeated observations.
	// TODO FIXME
	//ctx.OccurrenceMonitor = conf.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Get system details with golps
	info, err := host.Info()
	if err != nil {
		fmt.Printf("❌ Error retrieving host info: %v\n", err)
	}

	var hwModel string
	// Print SBC hardware details
	if conf.IsLinuxArm64() {
		hwModel = conf.GetBoardModel()
		// remove possible new line from hwModel
		hwModel = strings.TrimSpace(hwModel)
	} else {
		hwModel = "unknown"
	}

	// Print platform, OS etc. details
	fmt.Printf("System details: %s %s %s on %s hardware\n", info.OS, info.Platform, info.PlatformVersion, hwModel)

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	fmt.Printf("Starting analyzer in realtime mode. Threshold: %v, overlap: %v, sensitivity: %v, interval: %v\n",
		settings.BirdNET.Threshold,
		settings.BirdNET.Overlap,
		settings.BirdNET.Sensitivity,
		settings.Realtime.Interval)

	// Initialize database access.
	dataStore := datastore.New(settings)

	// Open a connection to the database and handle possible errors.
	if err := dataStore.Open(); err != nil {
		//logger.Error("main", "Failed to open database: %v", err)
		return err // Return error to stop execution if database connection fails.
	} else {
		//logger.Info("main", "Successfully opened database")
		// Ensure the database connection is closed when the function returns.
		defer closeDataStore(dataStore)
	}

	// Initialize the control channel for restart control.
	controlChan := make(chan string, 1)
	// Initialize the restart channel - now only used internally by RTSP streams
	// Sound card capture no longer uses this channel to prevent cross-source interference
	restartChan := make(chan struct{}, 10) // Increased buffer to prevent dropped restart signals
	// quitChannel is used to signal the goroutines to stop.
	quitChan := make(chan struct{})

	// audioLevelChan and soundLevelChan are already initialized as global variables at package level

	// Prepare sources list
	var sources []string
	if len(settings.Realtime.RTSP.URLs) > 0 || settings.Realtime.Audio.Source != "" {
		if len(settings.Realtime.RTSP.URLs) > 0 {
			sources = settings.Realtime.RTSP.URLs
		}
		if settings.Realtime.Audio.Source != "" {
			// We'll add malgo to sources only if device initialization succeeds
			// This will be handled in CaptureAudio
			sources = append(sources, "malgo")
		}

		// Initialize buffers for all audio sources
		if err := initializeBuffers(sources); err != nil {
			// If buffer initialization fails, log the error but continue
			// Some sources might still work
			log.Printf("⚠️  Error initializing buffers: %v", err)
			log.Println("⚠️  Some audio sources might not be available.")
		}
	} else {
		log.Println("⚠️  Starting without active audio sources. You can configure audio devices or RTSP streams through the web interface.")
	}

	// Queue is now initialized at package level in birdnet package
	// Optionally resize the queue if needed
	birdnet.ResizeQueue(5)

	// Initialize Prometheus metrics manager
	metrics, err := observability.NewMetrics()
	if err != nil {
		return fmt.Errorf("error initializing metrics: %w", err)
	}

	var birdImageCache *imageprovider.BirdImageCache
	if settings.Realtime.Dashboard.Thumbnails.Summary || settings.Realtime.Dashboard.Thumbnails.Recent {
		// Initialize the bird image cache
		birdImageCache = initBirdImageCache(dataStore, metrics)
	} else {
		birdImageCache = nil
	}

	// Initialize processor
	proc := processor.New(settings, dataStore, bn, metrics, birdImageCache)

	// Initialize and start the HTTP server
	httpServer := httpcontroller.New(settings, dataStore, birdImageCache, audioLevelChan, controlChan, proc, metrics)
	httpServer.Start()

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Initialize the buffer manager
	bufferManager := NewBufferManager(bn, quitChan, &wg)

	// Start buffer monitors for each audio source only if we have active sources
	if len(settings.Realtime.RTSP.URLs) > 0 || settings.Realtime.Audio.Source != "" {
		bufferManager.UpdateMonitors(sources)
	} else {
		log.Println("⚠️  Starting without active audio sources. You can configure audio devices or RTSP streams through the web interface.")
	}

	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, soundLevelChan)

	// start sound level publishers only if sound level monitoring is enabled
	if settings.Realtime.Audio.SoundLevel.Enabled {
		// start sound level MQTT publisher if MQTT is enabled
		if settings.Realtime.MQTT.Enabled {
			startSoundLevelMQTTPublisher(&wg, quitChan, proc)
		}

		// start sound level SSE publisher
		if httpServer.APIV2 != nil {
			startSoundLevelSSEPublisher(&wg, quitChan, httpServer.APIV2)
		}

		// start sound level metrics publisher
		startSoundLevelMetricsPublisher(&wg, quitChan, metrics)

		log.Println("🔊 Sound level monitoring enabled")
	} else {
		log.Println("🔇 Sound level monitoring disabled")
	}

	// Start RTSP health watchdog if we have RTSP streams
	if len(settings.Realtime.RTSP.URLs) > 0 {
		myaudio.StartRTSPHealthWatchdog()
		log.Println("🔍 Started RTSP health monitoring watchdog")
	}

	// start cleanup of clips
	if conf.Setting().Realtime.Audio.Export.Retention.Policy != "none" {
		startClipCleanupMonitor(&wg, quitChan, dataStore)
	}

	// start weather polling
	if settings.Realtime.Weather.Provider != "none" {
		startWeatherPolling(&wg, settings, dataStore, quitChan)
	}

	// start telemetry endpoint
	startTelemetryEndpoint(&wg, settings, metrics, quitChan)

	// start control monitor for hot reloads
	startControlMonitor(&wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc, httpServer)

	// start quit signal monitor
	monitorCtrlC(quitChan)

	// Track the HTTP server for clean shutdown
	var httpServerRef *httpcontroller.Server = httpServer

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			// Close controlChan to signal that no restart attempts should be made.
			close(controlChan)
			// Stop RTSP health watchdog
			myaudio.StopRTSPHealthWatchdog()
			// Stop all analysis buffer monitors
			bufferManager.RemoveAllMonitors()
			// Perform HLS resources cleanup
			log.Println("🧹 Cleaning up HLS resources before shutdown")
			if err := cleanupHLSStreamingFiles(); err != nil {
				log.Printf("⚠️ Warning: Failed to clean up HLS streaming files during shutdown: %v", err)
			}
			// Shut down HTTP server and clean up its resources
			if httpServerRef != nil {
				log.Println("🔌 Shutting down HTTP server")
				if err := httpServerRef.Shutdown(); err != nil {
					log.Printf("⚠️ Warning: Error shutting down HTTP server: %v", err)
				}
			}
			// Wait for all goroutines to finish.
			wg.Wait()
			// Delete the BirdNET interpreter.
			bn.Delete()
			// Return nil to indicate that the program exited successfully.
			return nil

		case <-restartChan:
			// Global restart is deprecated - each audio source manages its own restarts
			// RTSP streams have individual restart channels
			// Sound card capture doesn't restart
			if settings.Debug {
				fmt.Println("⚠️ Global restart signal received but ignored - audio sources manage their own lifecycle")
			}
		}
	}
}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, settings *conf.Settings, quitChan, restartChan chan struct{}, audioLevelChan chan myaudio.AudioLevelData, soundLevelChan chan myaudio.SoundLevelData) {
	// Stop previous demultiplexing goroutine if it exists
	audioDemuxManager.Stop()

	// Start new demux goroutine
	doneChan := audioDemuxManager.Start()

	// Create a unified audio channel
	unifiedAudioChan := make(chan myaudio.UnifiedAudioData, 100)
	go func() {
		defer audioDemuxManager.Done()

		// Convert unified audio data back to separate channels for existing handlers
		for {
			select {
			case <-doneChan:
				// Exit when signaled
				return
			case <-quitChan:
				// Exit when quit signal received
				return
			case unifiedData, ok := <-unifiedAudioChan:
				if !ok {
					// Channel closed, exit
					return
				}

				// Send audio level data to existing audio level channel with safety check
				select {
				case <-doneChan:
					return
				case <-quitChan:
					return
				case audioLevelChan <- unifiedData.AudioLevel:
				default:
					// Channel full, drop data
				}

				// Send sound level data to existing sound level channel if present
				if unifiedData.SoundLevel != nil {
					select {
					case <-doneChan:
						return
					case <-quitChan:
						return
					case soundLevelChan <- *unifiedData.SoundLevel:
					default:
						// Channel full, drop data
					}
				}
			}
		}
	}()

	// waitgroup is managed within CaptureAudio
	go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, unifiedAudioChan)
}

// startClipCleanupMonitor initializes and starts the clip cleanup monitoring routine in a new goroutine.
func startClipCleanupMonitor(wg *sync.WaitGroup, quitChan chan struct{}, dataStore datastore.Interface) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		clipCleanupMonitor(quitChan, dataStore)
	}()
}

// startWeatherPolling initializes and starts the weather polling routine in a new goroutine.
func startWeatherPolling(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, quitChan chan struct{}) {
	// Create new weather service
	weatherService, err := weather.NewService(settings, dataStore)
	if err != nil {
		log.Printf("⛈️ Failed to initialize weather service: %v", err)
		return
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		weatherService.StartPolling(quitChan)
	}()
}

func startTelemetryEndpoint(wg *sync.WaitGroup, settings *conf.Settings, metrics *observability.Metrics, quitChan chan struct{}) {
	// Initialize Prometheus metrics endpoint if enabled
	if settings.Realtime.Telemetry.Enabled {
		// Initialize metrics endpoint
		telemetryEndpoint, err := observability.NewEndpoint(settings, metrics)
		if err != nil {
			log.Printf("Error initializing telemetry endpoint: %v", err)
			return
		}

		// Start metrics server
		telemetryEndpoint.Start(wg, quitChan)
	}
}

// monitorCtrlC listens for the SIGINT (Ctrl+C) signal and triggers the application shutdown process.
func monitorCtrlC(quitChan chan struct{}) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT) // Register to receive SIGINT (Ctrl+C)

		<-sigChan // Block until a SIGINT signal is received

		log.Println("Received Ctrl+C, shutting down")
		close(quitChan) // Close the quit channel to signal other goroutines to stop
	}()
}

// closeDataStore attempts to close the database connection and logs the result.
func closeDataStore(store datastore.Interface) {
	if err := store.Close(); err != nil {
		log.Printf("Failed to close database: %v", err)
	} else {
		log.Println("Successfully closed database")
	}
}

// ClipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
func clipCleanupMonitor(quitChan chan struct{}, dataStore datastore.Interface) {
	// Create a ticker that triggers every five minutes to perform cleanup
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop() // Ensure the ticker is stopped to prevent leaks

	// Get the shared disk manager logger
	diskManagerLogger := diskmanager.GetLogger()

	policy := conf.Setting().Realtime.Audio.Export.Retention.Policy
	log.Println("Clip retention policy:", policy)
	diskManagerLogger.Info("Cleanup timer started",
		"policy", policy,
		"interval_minutes", 5,
		"timestamp", time.Now().Format(time.RFC3339))

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			diskManagerLogger.Info("Cleanup timer stopped",
				"reason", "quit signal received",
				"timestamp", time.Now().Format(time.RFC3339))
			// Ensure clean shutdown
			if err := diskmanager.CloseLogger(); err != nil {
				diskManagerLogger.Error("Failed to close diskmanager logger", "error", err)
			}
			return

		case t := <-ticker.C:
			log.Println("🧹 Running clip cleanup task")
			diskManagerLogger.Info("Cleanup timer triggered",
				"timestamp", t.Format(time.RFC3339),
				"policy", conf.Setting().Realtime.Audio.Export.Retention.Policy)

			// age based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "age" {
				diskManagerLogger.Debug("Starting age-based cleanup via timer")
				result := diskmanager.AgeBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					log.Printf("Error during age-based cleanup: %v", result.Err)
					diskManagerLogger.Error("Age-based cleanup failed",
						"error", result.Err,
						"timestamp", time.Now().Format(time.RFC3339))
				} else {
					log.Printf("🧹 Age-based cleanup completed successfully, clips removed: %d, current disk utilization: %d%%", result.ClipsRemoved, result.DiskUtilization)
					diskManagerLogger.Info("Age-based cleanup completed via timer",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization", result.DiskUtilization,
						"timestamp", time.Now().Format(time.RFC3339))
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "usage" {
				diskManagerLogger.Debug("Starting usage-based cleanup via timer")
				result := diskmanager.UsageBasedCleanup(quitChan, dataStore)
				if result.Err != nil {
					log.Printf("Error during usage-based cleanup: %v", result.Err)
					diskManagerLogger.Error("Usage-based cleanup failed",
						"error", result.Err,
						"timestamp", time.Now().Format(time.RFC3339))
				} else {
					log.Printf("🧹 Usage-based cleanup completed successfully, clips removed: %d, current disk utilization: %d%%", result.ClipsRemoved, result.DiskUtilization)
					diskManagerLogger.Info("Usage-based cleanup completed via timer",
						"clips_removed", result.ClipsRemoved,
						"disk_utilization", result.DiskUtilization,
						"timestamp", time.Now().Format(time.RFC3339))
				}
			}
		}
	}
}

// NOTE: Potential Race Condition: If multiple goroutines call this function concurrently,
// especially during initial startup, there's a risk of race conditions during provider
// registration (checking Get then Register is not atomic). Consider using sync.Once
// or ensuring this is called only once during a deterministic startup phase (e.g., in main).
// setupImageProviderRegistry initializes or retrieves the global image provider registry
// and registers the default providers (Wikimedia, AviCommons).
func setupImageProviderRegistry(ds datastore.Interface, metrics *observability.Metrics) (*imageprovider.ImageProviderRegistry, error) {
	// Use the global registry if available, otherwise create a new one
	var registry *imageprovider.ImageProviderRegistry
	if httpcontroller.ImageProviderRegistry != nil {
		registry = httpcontroller.ImageProviderRegistry
		log.Println("Using global image provider registry")
	} else {
		registry = imageprovider.NewImageProviderRegistry()
		httpcontroller.ImageProviderRegistry = registry // Assign back to global
		log.Println("Created new image provider registry")
	}

	var errs []error // Slice to collect errors

	// Attempt to register Wikimedia
	if _, ok := registry.GetCache("wikimedia"); !ok {
		wikiCache, err := imageprovider.CreateDefaultCache(metrics, ds)
		if err != nil {
			log.Printf("Failed to create WikiMedia image cache: %v", err)
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategoryImageProvider).
				Context("operation", "create_wikimedia_cache").
				Context("provider", "wikimedia").
				Build())
			// Continue even if one provider fails
		} else {
			if err := registry.Register("wikimedia", wikiCache); err != nil {
				log.Printf("Failed to register WikiMedia image provider: %v", err)
				errs = append(errs, errors.New(err).
					Component("realtime-analysis").
					Category(errors.CategoryImageProvider).
					Context("operation", "register_wikimedia_provider").
					Context("provider", "wikimedia").
					Build())
			} else {
				log.Println("Registered WikiMedia image provider")
			}
		}
	} else {
		log.Println("Using existing WikiMedia image provider")
	}

	// Attempt to register AviCommons
	if _, ok := registry.GetCache("avicommons"); !ok {
		log.Println("Attempting to register AviCommons provider...")

		// Debug logging for embedded filesystem if enabled
		if conf.Setting().Realtime.Dashboard.Thumbnails.Debug {
			log.Println("Embedded filesystem contents:")
			if err := fs.WalkDir(httpcontroller.ImageDataFs, ".", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					log.Printf("  Error walking path %s: %v", path, err)
					return nil
				}
				log.Printf("  %s (%v)", path, d.IsDir())
				return nil
			}); err != nil {
				log.Printf("Error walking embedded filesystem: %v", err)
			}
		}

		if err := imageprovider.RegisterAviCommonsProvider(registry, httpcontroller.ImageDataFs, metrics, ds); err != nil {
			log.Printf("Failed to register AviCommons provider: %v", err)
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategoryImageProvider).
				Context("operation", "register_avicommons_provider").
				Context("provider", "avicommons").
				Build())
			// Check if we can read the data file for debugging
			if _, errRead := fs.ReadFile(httpcontroller.ImageDataFs, "internal/imageprovider/data/latest.json"); errRead != nil {
				log.Printf("Error reading AviCommons data file: %v", errRead)
			} else {
				log.Println("AviCommons data file exists but provider registration failed.")
			}
		} else {
			log.Println("Successfully registered AviCommons image provider")
		}
	} else {
		log.Println("Using existing AviCommons image provider")
	}

	// Set the registry in each provider for fallback support
	registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
		cache.SetRegistry(registry)
		return true // Continue ranging
	})

	// Return joined errors if any occurred
	if len(errs) > 0 {
		return registry, errors.Join(errs...)
	}

	return registry, nil // No errors during setup
}

// selectDefaultImageProvider determines the default image provider based on configuration
func selectDefaultImageProvider(registry *imageprovider.ImageProviderRegistry) *imageprovider.BirdImageCache {
	preferredProvider := conf.Setting().Realtime.Dashboard.Thumbnails.ImageProvider
	var defaultCache *imageprovider.BirdImageCache

	if preferredProvider == "auto" {
		// Use wikimedia as the default provider in auto mode, if available
		defaultCache, _ = registry.GetCache("wikimedia")
		log.Println("Using WikiMedia as the default image provider (auto mode)")
	} else {
		// User has specified a specific provider
		if cache, ok := registry.GetCache(preferredProvider); ok {
			defaultCache = cache
			log.Printf("Using %s as the preferred image provider", preferredProvider)
		} else {
			// Fallback to wikimedia if preferred provider doesn't exist or isn't registered
			defaultCache, _ = registry.GetCache("wikimedia")
			log.Printf("Preferred provider '%s' not available, falling back to WikiMedia (if available)", preferredProvider)
		}
	}

	// If we still don't have a default cache (e.g., wikimedia failed registration), try any available provider.
	if defaultCache == nil {
		log.Println("No default image provider assigned yet, checking for any registered provider")
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			defaultCache = cache
			log.Printf("Using %s as the fallback default image provider", name)
			return false // Stop at the first provider found
		})
	}

	return defaultCache
}

// warmUpImageCacheInBackground fetches existing cache data and starts background tasks
// to fetch images for species not yet cached by any provider.
func warmUpImageCacheInBackground(ds datastore.Interface, registry *imageprovider.ImageProviderRegistry, defaultCache *imageprovider.BirdImageCache, speciesList []datastore.Note) {
	log.Println("Starting background image cache warm-up...")

	// Pre-fetch all cached image records from the database per provider
	allCachedImages := make(map[string]map[string]bool) // providerName -> scientificName -> exists
	if ds != nil {
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			providerCache, err := ds.GetAllImageCaches(name)
			if err != nil {
				log.Printf("Warning: Failed to get cached images for provider '%s': %v", name, err)
				return true // Continue to next provider
			}
			allCachedImages[name] = make(map[string]bool)
			for i := range providerCache {
				allCachedImages[name][providerCache[i].ScientificName] = true
			}
			log.Printf("Pre-fetched %d cached image records for provider '%s'", len(providerCache), name)
			return true // Continue ranging
		})
	} else {
		log.Println("Warning: Datastore is nil, cannot pre-fetch cached images.")
	}

	// Start background fetching of images for species not found in any cache
	go func() {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 5) // Limit to 5 concurrent fetches
		needsImage := 0

		for i := range speciesList {
			// Use direct access to name to avoid pointer allocation
			sciName := speciesList[i].ScientificName

			// Skip empty scientific names
			if sciName == "" {
				log.Printf("Warning: Skipping empty scientific name during image cache warm-up")
				continue
			}

			// Check if already cached by *any* provider
			alreadyCached := false
			for providerName := range allCachedImages {
				if _, exists := allCachedImages[providerName][sciName]; exists {
					alreadyCached = true
					break
				}
			}

			if alreadyCached {
				continue
			}

			needsImage++
			wg.Add(1)
			// The defaultCache.Get call below will handle initialization and locking.
			// No need to manually manipulate the Initializing map here.
			// defaultCache.Initializing.Store(sciName, struct{}{}) // REMOVED - Incorrect usage

			go func(name string) {
				defer wg.Done()
				// The tryInitialize function called by Get handles mutex cleanup.
				// defer defaultCache.Initializing.Delete(name) // REMOVED - Handled by tryInitialize
				sem <- struct{}{}
				defer func() { <-sem }()

				// Skip empty scientific names (double check)
				if name == "" {
					log.Printf("Warning: Caught empty scientific name in fetch goroutine")
					return
				}

				if _, err := defaultCache.Get(name); err != nil {
					log.Printf("Failed to fetch image for %s during warm-up: %v", name, err)
				}
			}(sciName) // Pass the captured name
		}

		if needsImage > 0 {
			log.Printf("Cache warm-up: %d species require image fetching.", needsImage)
			wg.Wait()
			log.Printf("Finished initializing BirdImageCache (%d species fetched/attempted)", needsImage)
		} else {
			log.Println("BirdImageCache initialized (all species images already present in DB cache)")
		}
	}()
}

// initBirdImageCache initializes the bird image cache by setting up providers,
// selecting a default, and starting a background warm-up process.
func initBirdImageCache(ds datastore.Interface, metrics *observability.Metrics) *imageprovider.BirdImageCache {
	// 1. Set up the registry and register known providers
	registry, regErr := setupImageProviderRegistry(ds, metrics)
	if regErr != nil {
		// Log errors encountered during provider registration
		log.Printf("Warning: Image provider registry initialization encountered errors: %v", regErr)
		// Note: We continue even if some providers fail, as others might succeed.
		// The selectDefaultImageProvider logic will handle finding an available provider.
	}

	// Defensive check: Ensure registry is not nil before proceeding.
	if registry == nil {
		log.Println("Error: Image provider registry could not be initialized.")
		return nil
	}

	// 2. Select the default cache based on settings and availability
	defaultCache := selectDefaultImageProvider(registry)

	// If no provider could be initialized or selected, return nil
	if defaultCache == nil {
		log.Println("Error: No image providers available or could be initialized.")
		return nil
	}

	// 3. Get the list of all detected species
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Printf("Failed to get detected species list: %v. Cache warm-up may be incomplete.", err)
		// Continue with an empty list if DB fails, warm-up won't happen
		speciesList = []datastore.Note{}
	}

	// Filter out any species with empty scientific names
	validSpeciesList := make([]datastore.Note, 0, len(speciesList))
	for i := range speciesList {
		if speciesList[i].ScientificName != "" {
			validSpeciesList = append(validSpeciesList, speciesList[i])
		} else {
			log.Printf("Warning: Found species entry with empty scientific name in database, skipping for image cache")
		}
	}

	if len(validSpeciesList) < len(speciesList) {
		log.Printf("Filtered %d species entries with empty scientific names from warm-up list", len(speciesList)-len(validSpeciesList))
	}

	// 4. Start the background cache warm-up process with validated species list
	warmUpImageCacheInBackground(ds, registry, defaultCache, validSpeciesList)

	return defaultCache
}

// startControlMonitor handles various control signals for realtime analysis mode
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor, httpServer *httpcontroller.Server) {
	monitor := NewControlMonitor(wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc, audioLevelChan, soundLevelChan)
	monitor.httpServer = httpServer
	monitor.Start()
}

// initializeBuffers handles initialization of all audio-related buffers
func initializeBuffers(sources []string) error {
	var initErrors []string

	// Initialize analysis buffers
	if err := myaudio.InitAnalysisBuffers(conf.BufferSize*3, sources); err != nil { // 3x buffer size to avoid underruns
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize analysis buffers: %v", err))
	}

	// Initialize capture buffers
	if err := myaudio.InitCaptureBuffers(60, conf.SampleRate, conf.BitDepth/8, sources); err != nil {
		initErrors = append(initErrors, fmt.Sprintf("failed to initialize capture buffers: %v", err))
	}

	if len(initErrors) > 0 {
		return fmt.Errorf("buffer initialization errors: %s", strings.Join(initErrors, "; "))
	}

	return nil
}

// cleanupHLSStreamingFiles removes any leftover HLS streaming files and directories
// from previous runs of the application to avoid accumulation of unused files.
func cleanupHLSStreamingFiles() error {
	// Get the HLS directory where all streaming files are stored
	hlsDir, err := conf.GetHLSDirectory()
	if err != nil {
		return fmt.Errorf("failed to get HLS directory: %w", err)
	}

	// Check if the directory exists
	_, err = os.Stat(hlsDir)
	if os.IsNotExist(err) {
		// Directory doesn't exist yet, nothing to clean up
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to check HLS directory: %w", err)
	}

	// Read the directory entries
	entries, err := os.ReadDir(hlsDir)
	if err != nil {
		return fmt.Errorf("failed to read HLS directory: %w", err)
	}

	var cleanupErrors []string

	// Remove all stream directories
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "stream_") {
			path := filepath.Join(hlsDir, entry.Name())
			log.Printf("🧹 Removing HLS stream directory: %s", path)

			// Remove the directory and all its contents
			if err := os.RemoveAll(path); err != nil {
				log.Printf("⚠️ Warning: Failed to remove HLS stream directory %s: %v", path, err)
				cleanupErrors = append(cleanupErrors, fmt.Sprintf("%s: %v", path, err))
				// Continue with other directories
			}
		}
	}

	// Return a combined error if any cleanup operations failed
	if len(cleanupErrors) > 0 {
		return fmt.Errorf("failed to remove some HLS stream directories: %s", strings.Join(cleanupErrors, "; "))
	}

	return nil
}

// startSoundLevelMQTTPublisher starts a goroutine to consume sound level data and publish to MQTT
func startSoundLevelMQTTPublisher(wg *sync.WaitGroup, quitChan chan struct{}, proc *processor.Processor) {
	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			select {
			case <-quitChan:
				return
			case soundData := <-soundLevelChan:
				// Publish sound level data to MQTT
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					log.Printf("❌ Error publishing sound level data to MQTT: %v", err)
				}
			}
		}
	}()
}

// publishSoundLevelToMQTT publishes sound level data to MQTT
func publishSoundLevelToMQTT(soundData myaudio.SoundLevelData, proc *processor.Processor) error {
	// Get current settings to determine MQTT topic
	settings := conf.Setting()
	if !settings.Realtime.MQTT.Enabled {
		return nil // MQTT not enabled, skip
	}

	// Create MQTT topic for sound level data
	topic := fmt.Sprintf("%s/soundlevel", strings.TrimSuffix(settings.Realtime.MQTT.Topic, "/"))

	// Marshal sound level data to JSON
	jsonData, err := json.Marshal(soundData)
	if err != nil {
		// Record error metric
		if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
			proc.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "mqtt", "marshal_error")
		}
		return errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "marshal_sound_level_data").
			Context("source", soundData.Source).
			Build()
	}

	// Publish to MQTT
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := proc.PublishMQTT(ctx, topic, string(jsonData)); err != nil {
		// Record error metric
		if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
			proc.Metrics.SoundLevel.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "mqtt", "publish_error")
			proc.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "mqtt", "error")
		}
		return errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "publish_sound_level_mqtt").
			Context("topic", topic).
			Context("source", soundData.Source).
			Build()
	}

	// Record success metric
	if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		proc.Metrics.SoundLevel.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "mqtt", "success")
	}

	log.Printf("📡 Published sound level data to MQTT topic: %s (source: %s, bands: %d)",
		topic, soundData.Source, len(soundData.OctaveBands))

	return nil
}

// startSoundLevelPublishers starts all sound level publishers with the given done channel
func startSoundLevelPublishers(wg *sync.WaitGroup, doneChan chan struct{}, proc *processor.Processor, soundLevelChan chan myaudio.SoundLevelData, httpServer *httpcontroller.Server) {
	settings := conf.Setting()

	// Create a merged quit channel that responds to both the done channel and global quit
	mergedQuitChan := make(chan struct{})
	go func() {
		<-doneChan
		close(mergedQuitChan)
	}()

	// Start MQTT publisher if enabled
	if settings.Realtime.MQTT.Enabled {
		startSoundLevelMQTTPublisherWithDone(wg, mergedQuitChan, proc, soundLevelChan)
	}

	// Start SSE publisher if API is available
	if httpServer != nil && httpServer.APIV2 != nil {
		startSoundLevelSSEPublisherWithDone(wg, mergedQuitChan, httpServer.APIV2, soundLevelChan)
	}

	// Start metrics publisher
	if proc.Metrics != nil && proc.Metrics.SoundLevel != nil {
		startSoundLevelMetricsPublisherWithDone(wg, mergedQuitChan, proc.Metrics, soundLevelChan)
	}
}

// startSoundLevelMQTTPublisherWithDone starts MQTT publisher with a custom done channel
func startSoundLevelMQTTPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, proc *processor.Processor, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("📡 Started sound level MQTT publisher")

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level MQTT publisher")
				return
			case soundData := <-soundLevelChan:
				if err := publishSoundLevelToMQTT(soundData, proc); err != nil {
					// Log with enhanced error (error already has telemetry context from publishSoundLevelToMQTT)
					log.Printf("❌ Error publishing sound level data to MQTT: %v", err)
				}
			}
		}
	}()
}

// startSoundLevelSSEPublisherWithDone starts SSE publisher with a custom done channel
func startSoundLevelSSEPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, apiController *api.Controller, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("📡 Started sound level SSE publisher")

		// Create a rate limiter: 1 log per minute
		errorLogLimiter := rate.NewLimiter(rate.Every(time.Minute), 1)

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level SSE publisher")
				return
			case soundData := <-soundLevelChan:
				if err := broadcastSoundLevelSSE(apiController, soundData); err != nil {
					// Only log errors if rate limiter allows
					if errorLogLimiter.Allow() {
						log.Printf("⚠️ Error broadcasting sound level data via SSE: %v", err)
					}
				}
			}
		}
	}()
}

// broadcastSoundLevelSSE broadcasts sound level data via SSE with error handling and metrics
func broadcastSoundLevelSSE(apiController *api.Controller, soundData myaudio.SoundLevelData) error {
	if err := apiController.BroadcastSoundLevel(&soundData); err != nil {
		// Record error metric
		if metrics := getSoundLevelMetricsFromAPI(apiController); metrics != nil {
			metrics.RecordSoundLevelPublishingError(soundData.Source, soundData.Name, "sse", "broadcast_error")
			metrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "error")
		}

		// Return enhanced error
		return errors.New(err).
			Component("realtime-analysis").
			Category(errors.CategoryNetwork).
			Context("operation", "broadcast_sound_level_sse").
			Context("source", soundData.Source).
			Context("name", soundData.Name).
			Context("bands_count", len(soundData.OctaveBands)).
			Build()
	}

	// Record success metric
	if metrics := getSoundLevelMetricsFromAPI(apiController); metrics != nil {
		metrics.RecordSoundLevelPublishing(soundData.Source, soundData.Name, "sse", "success")
	}

	return nil
}

// getSoundLevelMetricsFromAPI safely retrieves sound level metrics from API controller
func getSoundLevelMetricsFromAPI(apiController *api.Controller) *metrics.SoundLevelMetrics {
	if apiController == nil || apiController.Processor == nil ||
		apiController.Processor.Metrics == nil || apiController.Processor.Metrics.SoundLevel == nil {
		return nil
	}
	return apiController.Processor.Metrics.SoundLevel
}

// startSoundLevelMetricsPublisherWithDone starts metrics publisher with a custom done channel
func startSoundLevelMetricsPublisherWithDone(wg *sync.WaitGroup, doneChan chan struct{}, metrics *observability.Metrics, soundLevelChan chan myaudio.SoundLevelData) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("📊 Started sound level metrics publisher")

		for {
			select {
			case <-doneChan:
				log.Println("🔌 Stopping sound level metrics publisher")
				return
			case soundData := <-soundLevelChan:
				// Update Prometheus metrics
				if metrics != nil && metrics.SoundLevel != nil {
					updateSoundLevelMetrics(soundData, metrics)
				}
			}
		}
	}()
}

// registerSoundLevelProcessorsForActiveSources registers sound level processors for all active audio sources
func registerSoundLevelProcessorsForActiveSources(settings *conf.Settings) error {
	var errs []error

	// Register for malgo source if active
	if settings.Realtime.Audio.Source != "" {
		if err := myaudio.RegisterSoundLevelProcessor("malgo", settings.Realtime.Audio.Source); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "malgo").
				Context("source_name", settings.Realtime.Audio.Source).
				Build())
		} else {
			log.Printf("🔊 Registered sound level processor for audio device: %s", settings.Realtime.Audio.Source)
		}
	}

	// Register for each RTSP source
	for _, url := range settings.Realtime.RTSP.URLs {
		displayName := conf.SanitizeRTSPUrl(url)
		if err := myaudio.RegisterSoundLevelProcessor(url, displayName); err != nil {
			errs = append(errs, errors.New(err).
				Component("realtime-analysis").
				Category(errors.CategorySystem).
				Context("operation", "register_sound_level_processor").
				Context("source_type", "rtsp").
				Context("source_url", url).
				Build())
		} else {
			log.Printf("🔊 Registered sound level processor for RTSP source: %s", displayName)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// unregisterAllSoundLevelProcessors unregisters all sound level processors
func unregisterAllSoundLevelProcessors(settings *conf.Settings) {
	// Unregister malgo source
	if settings.Realtime.Audio.Source != "" {
		myaudio.UnregisterSoundLevelProcessor("malgo")
		log.Printf("🔇 Unregistered sound level processor for audio device: %s", settings.Realtime.Audio.Source)
	}

	// Unregister all RTSP sources
	for _, url := range settings.Realtime.RTSP.URLs {
		myaudio.UnregisterSoundLevelProcessor(url)
		log.Printf("🔇 Unregistered sound level processor for RTSP source: %s", conf.SanitizeRTSPUrl(url))
	}
}
