package analysis

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/imageprovider"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/handlers"
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

	// init detection queue
	queue.Init(5, 5)

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

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			// Close controlChan to signal that no restart attempts should be made.
			close(controlChan)
			// Stop all analysis buffer monitors
			bufferManager.RemoveAllMonitors()
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

	log.Println("Clip retention policy:", conf.Setting().Realtime.Audio.Export.Retention.Policy)

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			return

		case <-ticker.C:
			// age based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "age" {
				if err := diskmanager.AgeBasedCleanup(quitChan, dataStore); err != nil {
					log.Printf("Error during age-based cleanup: %v", err)
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "usage" {
				if err := diskmanager.UsageBasedCleanup(quitChan, dataStore); err != nil {
					log.Printf("Error during usage-based cleanup: %v", err)
				}
			}
		}
	}
}

// initBirdImageCache initializes the bird image cache by fetching all detected species from the database.
func initBirdImageCache(ds datastore.Interface, metrics *telemetry.Metrics) *imageprovider.BirdImageCache {
	// Create the cache first
	birdImageCache, err := imageprovider.CreateDefaultCache(metrics, ds)
	if err != nil {
		log.Printf("Failed to create image cache: %v", err)
		return nil
	}

	// Get the list of all detected species
	speciesList, err := ds.GetAllDetectedSpecies()
	if err != nil {
		log.Printf("Failed to get detected species: %v", err)
		return birdImageCache // Return the cache even if we can't get species list
	}

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
					log.Printf("Failed to fetch image for %s: %v", name, err)
				}
			}(species.ScientificName)
		}

		if needsImage > 0 {
			// Wait for all goroutines to complete
			wg.Wait()
			log.Printf("Finished initializing BirdImageCache (%d species needed images)", needsImage)
		} else {
			log.Println("BirdImageCache initialized (all species images already cached)")
		}
	}()

	return birdImageCache
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
