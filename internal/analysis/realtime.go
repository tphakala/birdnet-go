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

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

const (
	bytesPerSample = conf.BitDepth / 8
	bufferSize     = (conf.SampleRate * conf.NumChannels * conf.CaptureLength) * bytesPerSample
)

// RealtimeAnalysis initiates the BirdNET Analyzer in real-time mode and waits for a termination signal.
func RealtimeAnalysis(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter.
	bn, err := birdnet.NewBirdNET(settings)
	if err != nil {
		return fmt.Errorf("failed to initialize BirdNET: %w", err)
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
	fmt.Printf("Starting analyzer in realtime mode. Threshold: %v, sensitivity: %v, interval: %v\n",
		settings.BirdNET.Threshold,
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
	controlChannel := make(chan struct{}, 1)
	// Initialize the restart channel for capture restart control.
	restartChan := make(chan struct{})
	// quitChannel is used to signal the goroutines to stop.
	quitChan := make(chan struct{})

	// Initialize the ring buffer.
	myaudio.InitRingBuffer(bufferSize)

	// Audio buffer for extended audio clip capture
	audioBuffer := myaudio.NewAudioBuffer(60, conf.SampleRate, conf.BitDepth/8)

	// init detection queue
	queue.Init(5, 5)

	// Initialize Prometheus metrics manager
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		log.Fatalf("Error initializing metrics: %v", err)
	}

	// Start worker pool for processing detections
	processor.New(settings, dataStore, bn, audioBuffer, metrics)

	// Start http server
	httpcontroller.New(settings, dataStore)

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup
	// start buffer monitor
	startBufferMonitor(&wg, bn, quitChan)

	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioBuffer)

	// start cleanup of clips
	if conf.Setting().Realtime.Audio.Export.Retention.Enabled {
		startClipCleanupMonitor(&wg, settings, dataStore, quitChan)
	}

	// start telemetry endpoint
	startTelemetryEndpoint(&wg, settings, metrics, quitChan)

	// start quit signal monitor
	monitorCtrlC(quitChan)

	// loop to monitor quit and restart channels
	for {
		select {
		case <-quitChan:
			// Close controlChannel to signal that no restart attempts should be made.
			close(controlChannel)
			// Wait for all goroutines to finish.
			wg.Wait()
			// Delete the BirdNET interpreter.
			bn.Delete()
			// Return nil to indicate that the program exited successfully.
			return nil

		case <-restartChan:
			// Handle the restart signal.
			fmt.Println("Restarting audio capture")
			startAudioCapture(&wg, settings, quitChan, restartChan, audioBuffer)
		}
	}

}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, settings *conf.Settings, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *myaudio.AudioBuffer) {
	wg.Add(1)
	go myaudio.CaptureAudio(settings, wg, quitChan, restartChan, audioBuffer)
}

// startBufferMonitor initializes and starts the buffer monitoring routine in a new goroutine.
func startBufferMonitor(wg *sync.WaitGroup, bn *birdnet.BirdNET, quitChan chan struct{}) {
	wg.Add(1)
	go myaudio.BufferMonitor(wg, bn, quitChan)
}

// startClipCleanupMonitor initializes and starts the clip cleanup monitoring routine in a new goroutine.
func startClipCleanupMonitor(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, quitChan chan struct{}) {
	wg.Add(1)
	go clipCleanupMonitor(wg, dataStore, quitChan)
}

func startTelemetryEndpoint(wg *sync.WaitGroup, settings *conf.Settings, metrics *telemetry.Metrics, quitChan chan struct{}) {
	// Initialize Prometheus metrics endpoint if enabled
	if settings.Realtime.Telemetry.Enabled {
		// Initialize metrics endpoint
		telemetryEndpoint, err := telemetry.NewEndpoint(settings)
		if err != nil {
			log.Printf("Error initializing metrics manager: %v", err)
		}

		// Start metrics server
		telemetryEndpoint.Start(metrics, wg, quitChan)
	}
}

// monitorCtrlC listens for the SIGINT (Ctrl+C) signal and triggers the application shutdown process.
func monitorCtrlC(quitChan chan struct{}) {
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT) // Register to receive SIGINT (Ctrl+C)

		<-sigChan // Block until a SIGINT signal is received

		fmt.Println("\nReceived Ctrl+C, shutting down")
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

	for {
		select {
		case <-quitChan:
			// Handle quit signal to stop the monitor
			return

		case <-ticker.C:
			if conf.Setting().Realtime.Audio.Export.Debug {
				log.Println("Cleanup ticker triggered")
			}

			// age based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Mode == "age" {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Println("Running age based cleanup")
				}
				if err := diskmanager.AgeBasedCleanup(dataStore); err != nil {
					log.Println("Error cleaning up clips: ", err)
				}
			}

			// priority based cleanup method
			if conf.Setting().Realtime.Audio.Export.Retention.Mode == "priority" {
				if conf.Setting().Realtime.Audio.Export.Debug {
					log.Println("Running priority based cleanup")
				}
				if err := diskmanager.PriorityBasedCleanup(quitChan); err != nil {
					log.Println("Error cleaning up clips: ", err)
				}
			}
		}
	}
}
