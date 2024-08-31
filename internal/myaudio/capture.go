package myaudio

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// captureSource holds information about an audio capture source.
type captureSource struct {
	Name    string
	ID      string
	Pointer unsafe.Pointer
}

// AudioDeviceInfo holds information about an audio device.
type AudioDeviceInfo struct {
	Index int
	Name  string
	ID    string
}

// ListAudioSources returns a list of available audio capture devices.
func ListAudioSources() ([]AudioDeviceInfo, error) {
	fmt.Println("Listing audio sources")
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize context: %v", err)
	}
	defer ctx.Uninit()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %v", err)
	}

	var devices []AudioDeviceInfo
	for i, info := range infos {
		decodedID, err := hexToASCII(info.ID.String())
		if err != nil {
			fmt.Printf("Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		fmt.Printf("Device %d: %s, ID: %s\n", i, info.Name(), decodedID)

		devices = append(devices, AudioDeviceInfo{
			Index: i,
			Name:  info.Name(),
			ID:    decodedID,
		})
	}

	fmt.Println("Available devices:", devices)

	return devices, nil
}

// SetAudioDevice sets the audio device based on the provided index.
func SetAudioDevice(deviceName string) (string, error) {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to initialize context: %v", err)
	}

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		ctx.Uninit()
		return "", fmt.Errorf("failed to get devices: %v", err)
	}

	var index int
	for i, info := range infos {
		// Decode the device ID from hex to ASCII
		decodedID, err := hexToASCII(info.ID.String())
		if err != nil {
			log.Printf("Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		// Prepare the output string for listing available devices
		output := fmt.Sprintf("  %d: %s", i, info.Name())
		if runtime.GOOS == "linux" {
			output = fmt.Sprintf("%s, %s", output, decodedID) // Include decoded ID in the output for Linux
		}

		// Determine if the current device matches the specified settings
		if matchesDeviceSettings(decodedID, info, deviceName) {
			index = i
			break
		}
	}

	if index < 0 || index >= len(infos) {
		ctx.Uninit()
		return "", fmt.Errorf("invalid device index")
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Capture.DeviceID = infos[index].ID.Pointer()

	// Initialize the device
	_, err = malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{})
	if err != nil {
		ctx.Uninit()
		return "", fmt.Errorf("failed to initialize device: %v", err)
	}

	return infos[index].Name(), nil
}

func CaptureAudio(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}) {
	if len(settings.Realtime.RTSP.URLs) > 0 {
		// RTSP audio capture for each URL
		for _, url := range settings.Realtime.RTSP.URLs {
			wg.Add(1)
			go CaptureAudioRTSP(url, settings.Realtime.RTSP.Transport, wg, quitChan, restartChan)
		}
	} else {
		// Default audio capture
		wg.Add(1)
		captureAudioMalgo(settings, wg, quitChan, restartChan)
	}
}

// selectCaptureSource selects an appropriate capture device based on the provided settings and available device information.
// It prints available devices and returns the selected device and any error encountered.
func selectCaptureSource(settings *conf.Settings, infos []malgo.DeviceInfo) (captureSource, error) {
	fmt.Println("Available Capture Sources:")

	var selectedSource captureSource
	var deviceFound bool

	for i, info := range infos {
		// Decode the device ID from hex to ASCII
		decodedID, err := hexToASCII(info.ID.String())
		if err != nil {
			fmt.Printf("Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		// Prepare the output string for listing available devices
		output := fmt.Sprintf("  %d: %s", i, info.Name())
		if runtime.GOOS == "linux" {
			output = fmt.Sprintf("%s, %s", output, decodedID) // Include decoded ID in the output for Linux
		}

		// Determine if the current device matches the specified settings
		if matchesDeviceSettings(decodedID, info, settings.Realtime.Audio.Source) {
			selectedSource = captureSource{
				Name:    info.Name(),
				ID:      decodedID,
				Pointer: info.ID.Pointer(),
			}
			deviceFound = true
		}

		fmt.Println(output)
	}

	// If no device was found, return an error
	if !deviceFound {
		return captureSource{}, fmt.Errorf("no suitable capture source found for device setting %s", settings.Realtime.Audio.Source)
	}

	return selectedSource, nil
}

// matchesDeviceSettings checks if the device matches the settings specified by the user.
func matchesDeviceSettings(decodedID string, info malgo.DeviceInfo, audioSource string) bool {
	if runtime.GOOS == "windows" && audioSource == "sysdefault" {
		// On Windows, there is no "sysdefault" device. Use miniaudio's default device instead.
		return info.IsDefault == 1
	}
	// Check if the decoded ID or device name matches the user's setting.
	return decodedID == audioSource || strings.Contains(info.Name(), audioSource)
}

// hexToASCII converts a hexadecimal string to an ASCII string.
func hexToASCII(hexStr string) (string, error) {
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err // return the error if hex decoding fails
	}
	return string(bytes), nil
}

func captureAudioMalgo(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}) {
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
	defer malgoCtx.Uninit() //nolint:errcheck

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1

	var infos []malgo.DeviceInfo

	// Get list of capture sources
	infos, err = malgoCtx.Devices(malgo.Capture)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// Select the capture source based on the settings
	captureSource, err := selectCaptureSource(settings, infos)
	if err != nil {
		log.Fatalf("Error selecting capture source: %v", err)
		panic(err)
	}
	deviceConfig.Capture.DeviceID = captureSource.Pointer

	// Write to ringbuffer when audio data is received
	// BufferMonitor() will poll this buffer and read data from it
	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		WriteToAnalysisBuffer("malgo", pSamples)
		WriteToCaptureBuffer("malgo", pSamples)
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
	defer device.Stop() //nolint:errcheck

	if settings.Debug {
		fmt.Println("Device started")
	}
	// print audio device we are attached to
	fmt.Printf("Listening on source: %s (%s)\n", captureSource.Name, captureSource.ID)

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
