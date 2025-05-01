package analysis

import (
	"errors"
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
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// audioLevelChan is a channel to send audio level updates
var audioLevelChan = make(chan myaudio.AudioLevelData, 100)

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
	// Initialize the restart channel for capture restart control.
	restartChan := make(chan struct{}, 3)
	// quitChannel is used to signal the goroutines to stop.
	quitChan := make(chan struct{})

	// Initialize audioLevelChan, used to visualize audio levels on web ui
	audioLevelChan = make(chan myaudio.AudioLevelData, 100)

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
	metrics, err := telemetry.NewMetrics()
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
	httpServer := httpcontroller.New(settings, dataStore, birdImageCache, audioLevelChan, controlChan, proc)
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
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan)

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
	startControlMonitor(&wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc)

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
			// Handle the restart signal.
			fmt.Println("🔄 Restarting audio capture")
			startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan)
		}
	}
}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, settings *conf.Settings, quitChan, restartChan chan struct{}, audioLevelChan chan myaudio.AudioLevelData) {
	// waitgroup is managed within CaptureAudio
	go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, audioLevelChan)
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

func startTelemetryEndpoint(wg *sync.WaitGroup, settings *conf.Settings, metrics *telemetry.Metrics, quitChan chan struct{}) {
	// Initialize Prometheus metrics endpoint if enabled
	if settings.Realtime.Telemetry.Enabled {
		// Initialize metrics endpoint
		telemetryEndpoint, err := telemetry.NewEndpoint(settings, metrics)
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
func setupImageProviderRegistry(ds datastore.Interface, metrics *telemetry.Metrics) (*imageprovider.ImageProviderRegistry, error) {
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
			errMsg := fmt.Sprintf("Failed to create WikiMedia image cache: %v", err)
			log.Println(errMsg)
			errs = append(errs, errors.New(errMsg))
			// Continue even if one provider fails
		} else {
			if err := registry.Register("wikimedia", wikiCache); err != nil {
				errMsg := fmt.Sprintf("Failed to register WikiMedia image provider: %v", err)
				log.Println(errMsg)
				errs = append(errs, errors.New(errMsg))
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
			errMsg := fmt.Sprintf("Failed to register AviCommons provider: %v", err)
			log.Println(errMsg)
			errs = append(errs, errors.New(errMsg))
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
func initBirdImageCache(ds datastore.Interface, metrics *telemetry.Metrics) *imageprovider.BirdImageCache {
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
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{}, notificationChan chan handlers.Notification, bufferManager *BufferManager, proc *processor.Processor) {
	monitor := NewControlMonitor(wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc)
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
