package myaudio

import (
	"fmt"
	"log"

	"github.com/gen2brain/malgo"
	"github.com/smallnest/ringbuffer"
)

const (
	sampleRate    = 48000
	channelCount  = 1
	frameCount    = 1024
	captureLength = 3 // in seconds
	bufferSize    = sampleRate * channelCount * 2 * captureLength
)

func StartGoRoutines(debug *bool) {
	ringBuffer = ringbuffer.New(3 * sampleRate * channelCount)
	go CaptureAudio(debug)
	go BufferMonitor(debug)
}

func CaptureAudio(debug *bool) {
	if *debug {
		fmt.Println("Initializing context")
	}
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		if *debug {
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

	//sampleSize := malgo.SampleSizeInBytes(deviceConfig.Capture.Format)
	//fmt.Println("Sample size: ", sampleSize)

	// Write to ringbuffer when audio data is received
	// BufferMonitor() will poll this buffer and read data from it
	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		writeToBuffer(pSamples)
	}

	// Device callback to assign function to call when audio data is received
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		log.Fatalf("Device init failed %v", err)
	}

	if *debug {
		fmt.Println("Starting device")
	}
	err = device.Start()
	if err != nil {
		log.Fatalf("Device start failed %v", err)
	}
	if *debug {
		fmt.Println("Device started")
	}

	// Let the Go routine run indefinitely to keep capturing audio
	select {}
}
