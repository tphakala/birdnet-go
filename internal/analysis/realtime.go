package analysis

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/imageprovider"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// audioLevelChan is a channel to send audio level updates
var audioLevelChan = make(chan myaudio.AudioLevelData, 100)

// RealtimeAnalysis analyzes audio in real-time mode
func RealtimeAnalysis(settings *conf.Settings, notificationChan chan handlers.Notification) error {
	var err error

	// Initialize the global logger if not already initialized
	config := logger.Config{
		Level:         viper.GetString("log.level"),
		Development:   settings.Debug,
		FilePath:      settings.Main.Log.Path,
		JSON:          false, // Use human-readable format for console
		ForceJSONFile: true,  // Force JSON format for file output
		DisableColor:  viper.GetBool("log.disable_color"),
		DisableCaller: true,
	}

	// Create a new logger instance, this will be passed to all components
	logger, err := logger.NewLogger(config)
	if err != nil {
		return fmt.Errorf("error initializing logger: %w", err)
	}

	// Create a component logger for the realtime analyzer
	coreLogger := logger.Named("core")
	coreLogger.Info("Starting real-time analysis")

	// Initialize BirdNET interpreter
	if err := initializeBirdNET(settings); err != nil {
		coreLogger.Error("Failed to initialize BirdNET", "error", err)
		return err
	}

	// Initialize occurrence monitor to filter out repeated observations.
	// TODO FIXME
	//ctx.OccurrenceMonitor = conf.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Get system details with golps
	info, err := host.Info()
	if err != nil {
		coreLogger.Error("Failed to retrieve host info", "error", err)
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

	// Log system details
	coreLogger.Info("System details",
		"os", info.OS,
		"platform", info.Platform,
		"version", info.PlatformVersion,
		"hardware", hwModel)

	// Log analyzer configuration
	coreLogger.Info("Analyzer configuration",
		"threshold", settings.BirdNET.Threshold,
		"overlap", settings.BirdNET.Overlap,
		"sensitivity", settings.BirdNET.Sensitivity,
		"interval", settings.Realtime.Interval)

	// Initialize database access.
	dataStore := datastore.New(settings)

	// Set the logger for the datastore using a hierarchical name
	dbLogger := coreLogger.Named("db")
	dataStore.SetLogger(dbLogger)

	// Open a connection to the database and handle possible errors.
	if err := dataStore.Open(); err != nil {
		coreLogger.Error("Failed to open database", "error", err)
		return err // Return error to stop execution if database connection fails.
	} else {
		coreLogger.Info("Database connection established")
		// Ensure the database connection is closed when the function returns.
		defer closeDataStore(dataStore, coreLogger)
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
			coreLogger.Warn("Error initializing buffers", "error", err)
			coreLogger.Warn("Some audio sources might not be available")
		}
	} else {
		coreLogger.Warn("Starting without active audio sources. Configure audio devices or RTSP streams through the web interface.")
	}

	// init detection queue
	queue.Init(5, 5)

	// Initialize Prometheus metrics manager
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		coreLogger.Error("Failed to initialize metrics", "error", err)
		return fmt.Errorf("error initializing metrics: %w", err)
	}

	var birdImageCache *imageprovider.BirdImageCache
	if settings.Realtime.Dashboard.Thumbnails.Summary || settings.Realtime.Dashboard.Thumbnails.Recent {
		// Initialize the bird image cache
		birdImageCache = initBirdImageCache(dataStore, metrics, coreLogger.Named("imageCache"))
	} else {
		birdImageCache = nil
	}

	// Initialize processor with a logger
	// Create a child logger for the processor
	coreLogger.Named("processor")
	proc := processor.New(settings, dataStore, bn, metrics, birdImageCache)
	// Set logger for processor if it has a SetLogger method
	// This would require adding SetLogger method to processor if not exists
	// proc.SetLogger(analyzerLogger.Named("processor"))

	// Initialize and start the HTTP server with proper logger inheritance
	httpServer := httpcontroller.NewWithLogger(settings, dataStore, birdImageCache, audioLevelChan, controlChan, proc, coreLogger)
	httpServer.Start()

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Initialize the buffer manager with a logger
	// Create a child logger for the buffer manager
	coreLogger.Named("buffer")
	bufferManager := NewBufferManager(bn, quitChan, &wg)
	// Set logger for buffer manager if it has a SetLogger method
	// bufferManager.SetLogger(analyzerLogger.Named("buffer"))

	// Start buffer monitors for each audio source only if we have active sources
	if len(settings.Realtime.RTSP.URLs) > 0 || settings.Realtime.Audio.Source != "" {
		bufferManager.UpdateMonitors(sources)
	}

	// start audio capture with a logger
	audioLogger := coreLogger.Named("audio")
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, audioLogger)

	// start cleanup of clips with a logger
	cleanupLogger := coreLogger.Named("cleanup")
	if settings.Realtime.Audio.Export.Retention.Policy != "none" {
		startClipCleanupMonitor(&wg, quitChan, dataStore, settings, cleanupLogger)
	}

	// start weather polling with a logger
	weatherLogger := coreLogger.Named("weather")
	if settings.Realtime.Weather.Provider != "none" {
		startWeatherPolling(&wg, settings, dataStore, quitChan, weatherLogger)
	}

	// start telemetry endpoint with a logger
	telemetryLogger := coreLogger.Named("telemetry")
	startTelemetryEndpoint(&wg, settings, metrics, quitChan, telemetryLogger)

	// start control monitor for hot reloads with a logger
	controlLogger := coreLogger.Named("control")
	startControlMonitor(&wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc, controlLogger)

	// start quit signal monitor with a logger
	monitorCtrlC(quitChan, coreLogger)

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			coreLogger.Info("Shutting down realtime analyzer")
			// Close controlChan to signal that no restart attempts should be made.
			close(controlChan)
			// Stop all analysis buffer monitors
			bufferManager.RemoveAllMonitors()
			// Wait for all goroutines to finish.
			wg.Wait()
			// Delete the BirdNET interpreter.
			bn.Delete()
			coreLogger.Info("Realtime analyzer shutdown complete")
			// Return nil to indicate that the program exited successfully.
			return nil

		case <-restartChan:
			// Handle the restart signal.
			coreLogger.Info("Restarting audio capture")
			startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan, audioLogger)
		}
	}
}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, settings *conf.Settings, quitChan, restartChan chan struct{},
	audioLevelChan chan myaudio.AudioLevelData, logger *logger.Logger) {
	// Pass logger to CaptureAudio if it supports logger injection
	// This requires updating the CaptureAudio function signature
	// For now, we'll just log that we're starting audio capture
	logger.Info("Starting audio capture")

	// waitgroup is managed within CaptureAudio
	go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, audioLevelChan)
}

// startClipCleanupMonitor initializes and starts the clip cleanup monitoring routine in a new goroutine.
func startClipCleanupMonitor(wg *sync.WaitGroup, quitChan chan struct{}, dataStore datastore.Interface, settings *conf.Settings, logger *logger.Logger) {
	// Create a diskmanager instance with proper logger inheritance
	diskManagerLogger := logger.Named("diskmanager")
	dm := diskmanager.NewDiskManager(diskManagerLogger, dataStore)

	logger.Info("Starting clip cleanup monitor")

	wg.Add(1)
	go func() {
		defer wg.Done()
		clipCleanupMonitor(quitChan, dataStore, settings, logger, dm)
	}()
}

// ClipCleanupMonitor monitors the database and deletes clips that meet the retention policy.
func clipCleanupMonitor(quitChan chan struct{}, dataStore datastore.Interface, settings *conf.Settings, logger *logger.Logger, dm *diskmanager.DiskManager) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop() // Ensure the ticker is stopped to prevent leaks

	logger.Info("Starting clip cleanup monitor", "policy", settings.Realtime.Audio.Export.Retention.Policy)

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			logger.Info("Stopping clip cleanup monitor")
			return

		case <-ticker.C:
			// age based cleanup method
			if settings.Realtime.Audio.Export.Retention.Policy == "age" {
				logger.Debug("Running age-based cleanup")
				if err := dm.AgeBasedCleanup(quitChan); err != nil {
					logger.Error("Error during age-based cleanup", "error", err)
				}
			}

			// priority based cleanup method
			if settings.Realtime.Audio.Export.Retention.Policy == "usage" {
				logger.Debug("Running usage-based cleanup")
				if err := dm.UsageBasedCleanup(quitChan); err != nil {
					logger.Error("Error during usage-based cleanup", "error", err)
				}
			}
		}
	}
}

// startWeatherPolling initializes and starts the weather polling routine in a new goroutine.
func startWeatherPolling(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface,
	quitChan chan struct{}, logger *logger.Logger) {
	// Create new weather service
	weatherService, err := weather.NewService(settings, dataStore)
	if err != nil {
		logger.Error("Failed to initialize weather service", "error", err)
		return
	}

	// Set logger for weather service if it has a SetLogger method
	// weatherService.SetLogger(logger)

	logger.Info("Starting weather polling service")

	wg.Add(1)
	go func() {
		defer wg.Done()
		weatherService.StartPolling(quitChan)
	}()
}

func startTelemetryEndpoint(wg *sync.WaitGroup, settings *conf.Settings,
	metrics *telemetry.Metrics, quitChan chan struct{}, logger *logger.Logger) {
	// Initialize Prometheus metrics endpoint if enabled
	if settings.Realtime.Telemetry.Enabled {
		// Initialize metrics endpoint
		telemetryEndpoint, err := telemetry.NewEndpoint(settings, metrics)
		if err != nil {
			logger.Error("Failed to initialize telemetry endpoint", "error", err)
			return
		}

		// Set logger for telemetry endpoint if it has a SetLogger method
		// telemetryEndpoint.SetLogger(logger)

		logger.Info("Starting telemetry endpoint")

		// Start metrics server
		telemetryEndpoint.Start(wg, quitChan)
	} else {
		logger.Info("Telemetry endpoint disabled")
	}
}

// monitorCtrlC listens for the SIGINT (Ctrl+C) signal and triggers the application shutdown process.
func monitorCtrlC(quitChan chan struct{}, logger *logger.Logger) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT) // Register to receive SIGINT (Ctrl+C)

		<-sigChan // Block until a SIGINT signal is received

		logger.Info("Received shutdown signal (Ctrl+C)")
		close(quitChan) // Close the quit channel to signal other goroutines to stop
	}()
}

// closeDataStore attempts to close the database connection and logs the result.
func closeDataStore(store datastore.Interface, logger *logger.Logger) {
	if err := store.Close(); err != nil {
		logger.Error("Failed to close database", "error", err)
	} else {
		logger.Info("Database connection closed")
	}
}

// initBirdImageCache initializes the bird image cache by fetching all detected species from the database.
func initBirdImageCache(ds datastore.Interface, metrics *telemetry.Metrics, logger *logger.Logger) *imageprovider.BirdImageCache {
	logger.Info("Initializing bird image cache")

	// Create the cache first
	birdImageCache, err := imageprovider.CreateDefaultCache(metrics, ds, logger)
	if err != nil {
		logger.Error("Failed to create image cache", "error", err)
		return nil
	}

	// Get the list of all detected species
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		logger.Error("Failed to get detected species list", "error", err)
		return birdImageCache // Return the cache even if we can't get species list
	}

	logger.Info("Starting image cache initialization", "species_count", len(speciesList))

	// Start background fetching of images
	go func() {
		// Use a WaitGroup to wait for all goroutines to complete
		var wg sync.WaitGroup
		// Use a semaphore to limit concurrent fetches
		sem := make(chan struct{}, 5) // Limit to 5 concurrent fetches

		// Track how many species need images
		needsImage := 0

		for i := range speciesList {
			species := &speciesList[i] // Use pointer to avoid copying
			// Check if we already have this image cached
			if cached, err := ds.GetImageCache(species.ScientificName); err == nil && cached != nil {
				continue // Skip if already cached
			}

			needsImage++
			wg.Add(1)
			// Mark this species as being initialized
			birdImageCache.Initializing.Store(species.ScientificName, struct{}{})
			go func(name string) {
				defer func() {
					wg.Done()
				}()
				defer birdImageCache.Initializing.Delete(name) // Remove initialization mark when done
				sem <- struct{}{}                              // Acquire semaphore
				defer func() { <-sem }()                       // Release semaphore

				// Attempt to fetch the image for the given species
				if _, err := birdImageCache.Get(name); err != nil {
					logger.Warn("Failed to fetch image", "species", name, "error", err)
				}
			}(species.ScientificName)
		}

		if needsImage > 0 {
			// Wait for all goroutines to complete
			wg.Wait()
			logger.Info("Finished initializing image cache", "images_fetched", needsImage)
		} else {
			logger.Info("Image cache initialization complete", "status", "all species images already cached")
		}
	}()

	return birdImageCache
}

// startControlMonitor handles various control signals for realtime analysis mode
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan, restartChan chan struct{},
	notificationChan chan handlers.Notification, bufferManager *BufferManager,
	proc *processor.Processor, logger *logger.Logger) {
	logger.Info("Starting control monitor")

	monitor := NewControlMonitor(wg, controlChan, quitChan, restartChan, notificationChan, bufferManager, proc)
	// Set logger for control monitor if it has a SetLogger method
	// monitor.SetLogger(logger)

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
