package myaudio

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/config"
)

const (
	bitDepth       = 16    // for now only 16bit is supported
	sampleRate     = 48000 // BirdNET requires 48 kHz samples
	channelCount   = 1     // downmix to mono
	captureLength  = 3     // in seconds
	bytesPerSample = bitDepth / 8
	bufferSize     = (sampleRate * channelCount * captureLength) * bytesPerSample
)

// quitChannel is used to signal the capture goroutine to stop
var QuitChannel = make(chan struct{})

// restartChannel is used to signal the CaptureAudio goroutine to restart
var restartChannel = make(chan struct{})

func StartGoRoutines(ctx *config.Context) {
	var wg sync.WaitGroup
	controlChannel := make(chan struct{}, 1) // Buffered channel for restart control

	InitRingBuffer(bufferSize)

	wg.Add(1)
	go CaptureAudio(ctx, &wg)

	wg.Add(1)
	go BufferMonitor(ctx, &wg)

	go monitorCtrlC()

	for {
		select {
		case <-QuitChannel:
			close(controlChannel) // Signal to stop any restart attempts
			wg.Wait()
			return
		case <-restartChannel:
			// Restart signal received, restart CaptureAudio goroutine
			wg.Add(1)
			go CaptureAudio(ctx, &wg)
		}
	}
}

func monitorCtrlC() {
	// Set up channel to receive os signals
	sigChan := make(chan os.Signal, 1)
	// Notify sigChan on SIGINT (Ctrl+C)
	signal.Notify(sigChan, syscall.SIGINT)

	// Block until a signal is received
	<-sigChan

	fmt.Println("\nReceived Ctrl+C, shutting down")

	// When received, send a message to QuitChannel to clean up
	close(QuitChannel)
}

func CaptureAudio(ctx *config.Context, wg *sync.WaitGroup) {
	defer wg.Done() // Ensure this is called when the goroutine exits

	if ctx.Settings.Debug {
		fmt.Println("Initializing context")
	}
	malgoCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		if ctx.Settings.Debug {
			fmt.Print(message)
		}
	})
	if err != nil {
		log.Fatalf("context init failed %v", err)
	}
	defer malgoCtx.Uninit()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = channelCount
	deviceConfig.SampleRate = sampleRate
	deviceConfig.Alsa.NoMMap = 1

	// Write to ringbuffer when audio data is received
	// BufferMonitor() will poll this buffer and read data from it
	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		writeToBuffer(pSamples)
	}

	// onStopDevice is called when the device stops, either normally or unexpectedly
	onStopDevice := func() {
		select {
		case <-QuitChannel:
			// Quit signal has been received, do not signal for a restart
			return
		default:
			// No quit signal received, safe to signal for a restart
			fmt.Println("Audio device stopped, restarting capture")
			restartChannel <- struct{}{}
		}
	}

	// Device callback to assign function to call when audio data is received
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
		Stop: onStopDevice,
	}

	// Initialize the capture device
	device, err := malgo.InitDevice(malgoCtx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		log.Fatalf("Device init failed %v", err)
	}

	if ctx.Settings.Debug {
		fmt.Println("Starting device")
	}
	err = device.Start()
	if err != nil {
		log.Fatalf("Device start failed %v", err)
	}
	defer device.Stop()

	if ctx.Settings.Debug {
		fmt.Println("Device started")
	}
	fmt.Println("Listening ...")

	// Now, instead of directly waiting on QuitChannel,
	// check if it's closed in a non-blocking select.
	// This loop will keep running until QuitChannel is closed.
	for {
		select {
		case <-QuitChannel:
			// QuitChannel was closed, clean up and return.
			if ctx.Settings.Debug {
				fmt.Println("Stopping capture due to quit signal.")
			}
			return
		case <-restartChannel:
			// Handle restart signal
			if ctx.Settings.Debug {
				fmt.Println("Restarting capture.")
			}
			return
		default:
			// Do nothing and continue with the loop.
			// This default case prevents blocking if QuitChannel is not closed yet.
			// You may put a short sleep here to prevent a busy loop that consumes CPU unnecessarily.
			time.Sleep(100 * time.Millisecond)
		}
	}
}
