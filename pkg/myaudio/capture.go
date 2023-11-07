package myaudio

import (
	"fmt"
	"log"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/go-birdnet/pkg/config"
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

func StartGoRoutines(ctx *config.Context) {
	InitRingBuffer(bufferSize)
	go CaptureAudio(ctx)
	go BufferMonitor(ctx)
}

func CaptureAudio(ctx *config.Context) {
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

	// Device callback to assign function to call when audio data is received
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
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

	// Monitor the quitChannel and cleanup before exiting
	<-QuitChannel

	if ctx.Settings.Debug {
		fmt.Println("Stopping capture due to quit signal.")
	}
}
