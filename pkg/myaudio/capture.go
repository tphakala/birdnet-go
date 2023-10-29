package myaudio

import (
	"fmt"
	"log"

	"github.com/gen2brain/malgo"
	"github.com/smallnest/ringbuffer"
)

const (
	bitDepth       = 16    // for now only 16bit is supported
	sampleRate     = 48000 // BirdNET requires 48 kHz samples
	channelCount   = 1     // downmix to mono
	captureLength  = 3     // in seconds
	bytesPerSample = bitDepth / 8
	bufferSize     = (sampleRate * channelCount * captureLength) * bytesPerSample
)

func StartGoRoutines(debug *bool, capturePath *string, logPath *string, threshold *float64) {
	ringBuffer = ringbuffer.New(bufferSize)
	go CaptureAudio(debug)
	go BufferMonitor(debug, capturePath, logPath, threshold)
}

func CaptureAudio(debug *bool) {
	isDebug := *debug

	if isDebug {
		fmt.Println("Initializing context")
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		if isDebug {
			println(message)
		}
	})
	if err != nil {
		log.Fatalf("context init failed %v", err)
	}
	defer ctx.Uninit()

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
	device, err := malgo.InitDevice(ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		log.Fatalf("Device init failed %v", err)
	}

	if isDebug {
		fmt.Println("Starting device")
	}
	err = device.Start()
	if err != nil {
		log.Fatalf("Device start failed %v", err)
	}
	defer device.Stop()

	if isDebug {
		fmt.Println("Device started")
	}

	// Monitor the quitChannel and cleanup before exiting
	<-QuitChannel

	if isDebug {
		fmt.Println("Stopping capture due to quit signal.")
	}
}
