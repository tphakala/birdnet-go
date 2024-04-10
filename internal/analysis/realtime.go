package analysis

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/queue"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/httpcontroller"
	"github.com/tphakala/birdnet-go/internal/myaudio"
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
	//ctx.OccurrenceMonitor = conf.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	fmt.Println("Starting BirdNET-Go Analyzer in realtime mode")
	fmt.Printf("Threshold: %v, sensitivity: %v, interval: %v\n",
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
	audioBuffer := myaudio.NewAudioBuffer(30, conf.SampleRate, 2)

	// init detection queue
	queue.Init(5, 5)

	// Start worker pool for processing detections
	processor.New(settings, dataStore, bn, audioBuffer)

	// Start http server
	httpcontroller.New(settings, dataStore)

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup
	// start buffer monitor
	startBufferMonitor(&wg, bn, quitChan)
	// start audio capture
	startAudioCapture(&wg, settings, quitChan, restartChan, audioBuffer)
	// start cleanup of clips
	startClipCleanupMonitor(&wg, settings, dataStore, quitChan)

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
	go ClipCleanupMonitor(wg, settings, dataStore, quitChan)
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

// ClipCleanupMonitor monitors the database and deletes clips that meets the retention policy.
func ClipCleanupMonitor(wg *sync.WaitGroup, settings *conf.Settings, dataStore datastore.Interface, quitChan chan struct{}) {
	defer wg.Done()

	// Creating a ticker that ticks every 1 minute
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			// Quit signal received, stop the clip cleanup monitor
			return

		case <-ticker.C: // Wait for the next tick
			clipsForRemoval, _ := dataStore.GetClipsQualifyingForRemoval(settings.Realtime.Retention.MinEvictionHours, settings.Realtime.Retention.MinClipsPerSpecies)

			log.Printf("Found %d clips to remove\n", len(clipsForRemoval))

			for _, clip := range clipsForRemoval {
				if err := os.Remove(clip.ClipName); err != nil {
					log.Printf("Failed to remove %s: %s\n", clip.ClipName, err)
				} else {
					log.Printf("Removed %s\n", clip.ClipName)
				}
				dataStore.DeleteNoteClipPath(clip.ID)
			}
		}
	}
}
