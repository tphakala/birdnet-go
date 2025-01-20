package analysis

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v3/host"
	"github.com/tphakala/birdnet-go/internal/birdnet"
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
		fmt.Printf("Error retrieving host info: %v\n", err)
	}

	var hwModel string
	// Print SBC hardware details
	if conf.IsLinuxArm64() {
		hwModel = conf.GetBoardModel()
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

	// Initialize ring buffers for each audio source
	var sources []string
	if len(settings.Realtime.RTSP.URLs) > 0 {
		sources = settings.Realtime.RTSP.URLs
		// DEBUG
		//log.Println("RTSP sources configured, using RTSP for audio capture")
	} else {
		// DEBUG
		//log.Println("No RTSP sources configured, using malgo for audio capture")
		sources = []string{"malgo"}
	}

	// Initialize ring buffers for each audio source
	myaudio.InitRingBuffers(conf.BufferSize*3, sources) // 3x buffer size to avoid underruns

	// Audio buffer for extended audio clip capture
	myaudio.InitAudioBuffers(60, conf.SampleRate, conf.BitDepth/8, sources)

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

	// Start worker pool for processing detections
	processor.New(settings, dataStore, bn, metrics, birdImageCache)

	// Initialize and start the HTTP server
	httpServer := httpcontroller.New(settings, dataStore, birdImageCache, audioLevelChan, controlChan)
	httpServer.Start()

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup

	// Start buffer monitors for each audio source
	for _, source := range sources {
		wg.Add(1)
		go myaudio.BufferMonitor(&wg, bn, quitChan, source)
	}

	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioLevelChan)

	// start cleanup of clips
	if conf.Setting().Realtime.Audio.Export.Retention.Policy != "none" {
		startClipCleanupMonitor(&wg, settings, dataStore, quitChan)
	}

	// start weather polling
	if settings.Realtime.Weather.Provider != "none" {
		startWeatherPolling(&wg, settings, dataStore, quitChan)
	}

	// start telemetry endpoint
	startTelemetryEndpoint(&wg, settings, metrics, quitChan)

	// start control monitor for hot reloads
	startControlMonitor(&wg, controlChan, quitChan, notificationChan)

	// start quit signal monitor
	monitorCtrlC(quitChan)

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			// Close controlChan to signal that no restart attempts should be made.
			close(controlChan)
			// Wait for all goroutines to finish.
			wg.Wait()
			// Delete the BirdNET interpreter.
			bn.Delete()
			// Return nil to indicate that the program exited successfully.
			return nil

		case <-restartChan:
			// Handle the restart signal.
			fmt.Println("Restarting audio capture")
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
func startClipCleanupMonitor(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, quitChan chan struct{}) {
	wg.Add(1)
	go clipCleanupMonitor(wg, dataStore, quitChan)
}

// startWeatherPolling initializes and starts the weather polling routine in a new goroutine.
func startWeatherPolling(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, quitChan chan struct{}) {
	// Create new weather service
	weatherService, err := weather.NewService(settings, dataStore)
	if err != nil {
		log.Printf("Failed to initialize weather service: %v", err)
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
func clipCleanupMonitor(wg *sync.WaitGroup, dataStore datastore.Interface, quitChan chan struct{}) {
	defer wg.Done() // Ensure that the WaitGroup is marked as done after the function exits

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
				if err := diskmanager.AgeBasedCleanup(quitChan); err != nil {
					log.Println("Error cleaning up clips: ", err)
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Policy == "usage" {
				if err := diskmanager.UsageBasedCleanup(quitChan); err != nil {
					log.Println("Error cleaning up clips: ", err)
				}
			}
		}
	}
}

// initBirdImageCache initializes the bird image cache by fetching all detected species from the database.
func initBirdImageCache(ds datastore.Interface, metrics *telemetry.Metrics) *imageprovider.BirdImageCache {
	birdImageCache, err := imageprovider.CreateDefaultCache(metrics)
	if err != nil {
		log.Printf("Failed to create image cache: %v", err)
		return nil
	}

	// Initialize the image cache by fetching all detected species in the database
	go func() {
		// Retrieve the list of all detected species from the datastore
		speciesList, err := ds.GetAllDetectedSpecies()
		if err != nil {
			log.Printf("Failed to get detected species: %v", err)
			return
		}

		//log.Printf("Fetching images for %d species", len(speciesList))

		// Use a WaitGroup to wait for all goroutines to complete
		var wg sync.WaitGroup

		for _, species := range speciesList {
			wg.Add(1)

			// Launch a goroutine to fetch the image for each species
			go func(speciesName string) {
				defer wg.Done()
				// Attempt to fetch the image for the given species
				_, _ = birdImageCache.Get(speciesName)
				/* TODO add debug flag to image cache and enable prints only if debug is true
				if err != nil {
					//log.Printf("Failed to get image for species %s: %v\n", speciesName, err)
				} else {
					//log.Printf("Successfully fetched image for species %s\n", speciesName)
				}*/
			}(species.ScientificName)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		log.Println("Finished initializing BirdImageCache")
	}()

	return birdImageCache
}

// startControlMonitor handles various control signals for realtime analysis mode
func startControlMonitor(wg *sync.WaitGroup, controlChan chan string, quitChan chan struct{}, notificationChan chan handlers.Notification) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case signal := <-controlChan:
				switch signal {
				case "rebuild_range_filter":
					if err := birdnet.BuildRangeFilter(bn); err != nil {
						log.Printf("\033[31m❌ Error handling range filter rebuild: %v\033[0m", err)
						notificationChan <- handlers.Notification{
							Message: fmt.Sprintf("Failed to rebuild range filter: %v", err),
							Type:    "error",
						}
					} else {
						log.Printf("\033[32m🔄 Range filter rebuilt successfully\033[0m")
						notificationChan <- handlers.Notification{
							Message: "Range filter rebuilt successfully",
							Type:    "success",
						}
					}
				case "reload_birdnet":
					if err := bn.ReloadModel(); err != nil {
						log.Printf("\033[31m❌ Error reloading BirdNET model: %v\033[0m", err)
						notificationChan <- handlers.Notification{
							Message: fmt.Sprintf("Failed to reload BirdNET model: %v", err),
							Type:    "error",
						}
					} else {
						log.Printf("\033[32m✅ BirdNET model reloaded successfully\033[0m")
						notificationChan <- handlers.Notification{
							Message: "BirdNET model reloaded successfully",
							Type:    "success",
						}
						// Rebuild range filter after model reload
						if err := birdnet.BuildRangeFilter(bn); err != nil {
							log.Printf("\033[31m❌ Error rebuilding range filter after model reload: %v\033[0m", err)
							notificationChan <- handlers.Notification{
								Message: fmt.Sprintf("Failed to rebuild range filter: %v", err),
								Type:    "error",
							}
						} else {
							log.Printf("\033[32m✅ Range filter rebuilt successfully\033[0m")
							notificationChan <- handlers.Notification{
								Message: "Range filter rebuilt successfully",
								Type:    "success",
							}
						}
					}
				default:
					log.Printf("Received unknown control signal: %v", signal)
				}
			case <-quitChan:
				return
			}
		}
	}()
}
