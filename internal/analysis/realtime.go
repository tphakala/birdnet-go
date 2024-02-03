package analysis

import (
	"fmt"
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
func RealtimeAnalysis(ctx *conf.Context) error {

	// Initialize the BirdNET interpreter.
	bn, err := birdnet.NewBirdNET(ctx.Settings)
	if err != nil {
		return fmt.Errorf("failed to initialize BirdNET: %w", err)
	}

	// Initialize occurrence monitor to filter out repeated observations.
	ctx.OccurrenceMonitor = conf.NewOccurrenceMonitor(time.Duration(ctx.Settings.Realtime.Interval) * time.Second)

	// Log the start of BirdNET-Go Analyzer in realtime mode and its configurations.
	fmt.Println("Starting BirdNET-Go Analyzer in realtime mode")
	fmt.Printf("Threshold: %v, sensitivity: %v, interval: %v\n",
		ctx.Settings.BirdNET.Threshold,
		ctx.Settings.BirdNET.Sensitivity,
		ctx.Settings.Realtime.Interval)

	// Initialize database access.
	dataStore := datastore.New(ctx)

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

	// init detection queue
	queue.Init(5, 5)

	// Start worker pool for processing detections
	processor.New(ctx, dataStore)

	// Start http server
	httpcontroller.New(ctx, dataStore)

	// Initialize the wait group to wait for all goroutines to finish
	var wg sync.WaitGroup
	// start buffer monitor
	startBufferMonitor(&wg, ctx, bn, quitChan)
	// start audio capture
	startAudioCapture(&wg, ctx, quitChan, restartChan)

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
			startAudioCapture(&wg, ctx, quitChan, restartChan)
		}
	}
}

// startAudioCapture initializes and starts the audio capture routine in a new goroutine.
func startAudioCapture(wg *sync.WaitGroup, ctx *conf.Context, quitChan chan struct{}, restartChan chan struct{}) {
	wg.Add(1)
	go myaudio.CaptureAudio(ctx, wg, quitChan, restartChan)
}

// startBufferMonitor initializes and starts the buffer monitoring routine in a new goroutine.
func startBufferMonitor(wg *sync.WaitGroup, ctx *conf.Context, bn *birdnet.BirdNET, quitChan chan struct{}) {
	wg.Add(1)
	go myaudio.BufferMonitor(ctx, wg, bn, quitChan)
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
		//logger.Error("main", "Failed to close database: %v", err)
	} else {
		//logger.Info("main", "Successfully closed database")
	}
}
