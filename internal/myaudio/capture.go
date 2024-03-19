package myaudio

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func CaptureAudio(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *AudioBuffer) {
	if settings.Realtime.RTSP.Url != "" {
		// RTSP audio capture
		captureAudioRTSP(settings, wg, quitChan, restartChan, audioBuffer)
	} else {
		// Default audio capture
		captureAudioMalgo(settings, wg, quitChan, restartChan, audioBuffer)
	}
}

func captureAudioMalgo(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *AudioBuffer) {
	defer wg.Done() // Ensure this is called when the goroutine exits
	var device *malgo.Device

	if settings.Debug {
		fmt.Println("Initializing context")
	}

	// if Linux set malgo.BackendAlsa, else set nil for auto select
	var backend malgo.Backend
	if runtime.GOOS == "linux" {
		backend = malgo.BackendAlsa
	} else if runtime.GOOS == "windows" {
		backend = malgo.BackendWasapi
	} else if runtime.GOOS == "darwin" {
		backend = malgo.BackendCoreaudio
	}

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, func(message string) {
		if settings.Debug {
			fmt.Print(message)
		}
	})
	if err != nil {
		log.Fatalf("context init failed %v", err)
	}
	defer malgoCtx.Uninit()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1

	var infos []malgo.DeviceInfo

	// Get list of capture devices
	infos, err = malgoCtx.Devices(malgo.Capture)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Capture Devices")
	for i, info := range infos {
		e := "ok"
		_, err := malgoCtx.DeviceInfo(malgo.Capture, info.ID, malgo.Shared)
		if err != nil {
			e = err.Error()
		}
		fmt.Printf("    %d: %s, %s, [%s]\n", i, info.Name(), info.ID.String(), e)
	}
	//selectedDeviceInfo := infos[2]
	//deviceConfig.Capture.DeviceID = selectedDeviceInfo.ID.Pointer()

	// Write to ringbuffer when audio data is received
	// BufferMonitor() will poll this buffer and read data from it
	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		WriteToBuffer(pSamples)
		audioBuffer.Write(pSamples)
	}

	// onStopDevice is called when the device stops, either normally or unexpectedly
	onStopDevice := func() {
		go func() {
			select {
			case <-quitChan:
				// Quit signal has been received, do not attempt to restart
				return
			case <-time.After(100 * time.Millisecond):
				// Wait a bit before restarting to avoid potential rapid restart loops
				if settings.Debug {
					fmt.Println("Attempting to restart audio device.")
				}
				err := device.Start()
				if err != nil {
					log.Printf("Failed to restart audio device: %v", err)
					log.Println("Attempting full audio context restart in 1 second.")
					time.Sleep(1 * time.Second)
					restartChan <- struct{}{}
				} else if settings.Debug {
					fmt.Println("Audio device restarted successfully.")
				}
			}
		}()
	}

	// Device callback to assign function to call when audio data is received
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
		Stop: onStopDevice,
	}

	// Initialize the capture device
	device, err = malgo.InitDevice(malgoCtx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		log.Printf("Device init failed %v", err)
		conf.PrintUserInfo()
		os.Exit(1)
	}

	if settings.Debug {
		fmt.Println("Starting device")
	}
	err = device.Start()
	if err != nil {
		log.Fatalf("Device start failed %v", err)
	}
	defer device.Stop()

	if settings.Debug {
		fmt.Println("Device started")
	}
	fmt.Println("Listening ...")

	// Now, instead of directly waiting on QuitChannel,
	// check if it's closed in a non-blocking select.
	// This loop will keep running until QuitChannel is closed.
	for {
		select {
		case <-quitChan:
			// QuitChannel was closed, clean up and return.
			if settings.Debug {
				fmt.Println("Stopping capture due to quit signal.")
			}
			return
		case <-restartChan:
			// Handle restart signal
			if settings.Debug {
				fmt.Println("Restarting capture.")
			}
			return
		default:
			// Do nothing and continue with the loop.
			// This default case prevents blocking if quitChan is not closed yet.
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func captureAudioRTSP(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioBuffer *AudioBuffer) {
	defer wg.Done()

	// Context to control the lifetime of the FFmpeg command
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Determine the RTSP transport protocol based on settings
	rtspTransport := "udp"
	if settings.Realtime.RTSP.Transport != "" {
		rtspTransport = settings.Realtime.RTSP.Transport
	}

	// Start FFmpeg with the configured settings
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", rtspTransport, // RTSP transport protocol (tcp/udp)
		"-i", settings.Realtime.RTSP.Url, // RTSP url
		"-loglevel", "error", // Suppress FFmpeg log output
		"-vn",         // No video
		"-f", "s16le", // 16-bit signed little-endian PCM
		"-ar", "48000", // Sample rate
		"-ac", "1", // Single channel (mono)
		"pipe:1", // Output raw audio data to standard out
	)

	// Capture FFmpeg's stdout for processing
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("Error creating ffmpeg pipe: %v", err)
	}

	// Attempt to start the FFmpeg process
	log.Println("Starting ffmpeg with command: ", cmd.String())
	if err := cmd.Start(); err != nil {
		log.Printf("Error starting FFmpeg: %v", err)
		return
	}

	// Ensure cmd.Wait() is called to clean up the process table entry on FFmpeg exit
	defer func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("FFmpeg wait error: %v", err)
		}
	}()

	// Start a goroutine to read from FFmpeg's stdout and write to the ring buffer
	go func() {
		// Ensure the FFmpeg process is terminated when this goroutine exits.
		defer cancel()

		// Buffer to hold the audio data read from FFmpeg's stdout.
		buf := make([]byte, 65536)
		for {
			n, err := stdout.Read(buf)
			// On read error, log the error, signal a restart, and exit the goroutine.
			if err != nil {
				log.Printf("Error reading from ffmpeg: %v", err)
				cancel()
				time.Sleep(3 * time.Second) // wait before restarting
				restartChan <- struct{}{}
				return
			}
			// Write to ring buffer when audio data is received
			WriteToBuffer(buf[:n])
			audioBuffer.Write(buf[:n])
		}
	}()

	// Stop here and wait for a quit signal or context cancellation (ffmpeg exit)
	for {
		select {
		case <-quitChan:
			log.Println("Quit signal received, stopping FFmpeg.")
			cancel()
			return
		case <-ctx.Done():
			// Context was cancelled, clean up and exit goroutine
			return
		}
	}
}
