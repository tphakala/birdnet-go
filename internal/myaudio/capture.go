package myaudio

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math"
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

// AudioLevelData holds audio level data
type AudioLevelData struct {
	Level    int  // 0-100
	Clipping bool // true if clipping is detected
}

// ListAudioSources returns a list of available audio capture devices.
func ListAudioSources() ([]AudioDeviceInfo, error) {
	// Initialize the audio context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize context: %w", err)
	}

	// Ensure the context is uninitialized when the function returns
	defer func() {
		if err := ctx.Uninit(); err != nil {
			log.Printf("failed to uninitialize context: %v", err)
		}
	}()

	// Get a list of capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}

	// Create a slice to store audio device information
	var devices []AudioDeviceInfo

	// Iterate through the list of devices
	for i, info := range infos {
		// Decode the device ID from hexadecimal to ASCII
		decodedID, err := hexToASCII(info.ID.String())
		if err != nil {
			log.Printf("Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		// Add the device information to the devices slice
		devices = append(devices, AudioDeviceInfo{
			Index: i,
			Name:  info.Name(),
			ID:    decodedID,
		})
	}

	// Return the list of devices and nil error
	return devices, nil
}

// SetAudioDevice sets the audio device based on the provided device name.
func SetAudioDevice(deviceName string) (string, error) {
	// Initialize the audio context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to initialize context: %w", err)
	}

	// Ensure the context is uninitialized when the function returns
	defer func() {
		if err := ctx.Uninit(); err != nil {
			log.Printf("failed to uninitialize context: %v", err)
		}
	}()

	// Get a list of capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return "", fmt.Errorf("failed to get devices: %w", err)
	}

	// Find the index of the device that matches the provided device name
	var index int
	for i, info := range infos {
		// Decode the device ID from hex to ASCII
		decodedID, err := hexToASCII(info.ID.String())
		if err != nil {
			log.Printf("Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		// Check if the current device matches the specified settings
		if matchesDeviceSettings(decodedID, info, deviceName) {
			index = i
			break
		}
	}

	// Check if a valid device was found
	if index < 0 || index >= len(infos) {
		return "", fmt.Errorf("invalid device index")
	}

	// Configure the device
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Capture.DeviceID = infos[index].ID.Pointer()

	// Initialize the device
	_, err = malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{})
	if err != nil {
		return "", fmt.Errorf("failed to initialize device: %w", err)
	}

	// Return the name of the selected device
	return infos[index].Name(), nil
}

func CaptureAudio(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	if len(settings.Realtime.RTSP.URLs) > 0 {
		// RTSP audio capture for each URL
		for _, url := range settings.Realtime.RTSP.URLs {
			wg.Add(1)
			go CaptureAudioRTSP(url, settings.Realtime.RTSP.Transport, wg, quitChan, restartChan, audioLevelChan)
		}
	} else {
		// Default audio capture
		wg.Add(1)
		captureAudioMalgo(settings, wg, quitChan, restartChan, audioLevelChan)
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

	// Check if running in container and only null device is available
	if conf.RunningInContainer() && len(infos) == 1 && strings.Contains(infos[0].Name(), "Discard all samples") {
		return captureSource{}, fmt.Errorf("no audio devices available in container. Please map host audio devices by running docker with: --device /dev/snd")
	}

	// If no device was found, print a message
	if !deviceFound {
		fmt.Printf("No suitable capture source found for device setting %s, please configure audio device in application settings\n", settings.Realtime.Audio.Source)
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

func captureAudioMalgo(settings *conf.Settings, wg *sync.WaitGroup, quitChan chan struct{}, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
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

	// Initialize the filter chain
	if err := InitializeFilterChain(settings); err != nil {
		log.Printf("Error initializing filter chain: %v", err)
	}

	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		// Apply audio EQ filters if enabled
		if settings.Realtime.Audio.Equalizer.Enabled {
			err := ApplyFilters(pSamples)
			if err != nil {
				log.Printf("Error applying audio EQ filters: %v", err)
			}
		}

		WriteToAnalysisBuffer("malgo", pSamples)
		WriteToCaptureBuffer("malgo", pSamples)

		// Calculate audio level
		audioLevelData := calculateAudioLevel(pSamples)

		// Send level to channel (non-blocking)
		select {
		case audioLevelChan <- audioLevelData:
			// Data sent successfully
		default:
			// Channel is full, clear the channel
			for len(audioLevelChan) > 0 {
				<-audioLevelChan
			}
			// Try to send the new data
			audioLevelChan <- audioLevelData
		}
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

// calculateAudioLevel calculates the RMS (Root Mean Square) of the audio samples
// and returns an AudioLevelData struct with the level and clipping status
func calculateAudioLevel(samples []byte) AudioLevelData {
	// If there are no samples, return zero level and no clipping
	if len(samples) == 0 {
		return AudioLevelData{Level: 0, Clipping: false}
	}

	// Ensure we have an even number of bytes (16-bit samples)
	if len(samples)%2 != 0 {
		// Truncate to even number of bytes
		samples = samples[:len(samples)-1]
	}

	var sum float64
	sampleCount := len(samples) / 2 // 2 bytes per sample for 16-bit audio
	isClipping := false
	maxSample := float64(0)

	// Iterate through samples, calculating sum of squares and checking for clipping
	for i := 0; i < len(samples); i += 2 {
		if i+1 >= len(samples) {
			break
		}

		// Convert two bytes to a 16-bit sample
		sample := int16(binary.LittleEndian.Uint16(samples[i : i+2]))
		sampleAbs := math.Abs(float64(sample))
		sum += sampleAbs * sampleAbs

		// Keep track of the maximum sample value
		if sampleAbs > maxSample {
			maxSample = sampleAbs
		}

		// Check for clipping (maximum positive or negative 16-bit value)
		if sample == 32767 || sample == -32768 {
			isClipping = true
		}
	}

	// If we ended up with no samples, return zero level and no clipping
	if sampleCount == 0 {
		return AudioLevelData{Level: 0, Clipping: false}
	}

	// Calculate Root Mean Square (RMS)
	rms := math.Sqrt(sum / float64(sampleCount))

	// Convert RMS to decibels
	// 32768 is max value for 16-bit audio
	db := 20 * math.Log10(rms/32768.0)

	// Scale decibels to 0-100 range
	// Adjust the range to make it more sensitive
	scaledLevel := (db + 60) * (100.0 / 50.0)

	// If the audio is clipping, ensure the level is at or near 100
	if isClipping {
		scaledLevel = math.Max(scaledLevel, 95)
	}

	// Clamp the value between 0 and 100
	if scaledLevel < 0 {
		scaledLevel = 0
	} else if scaledLevel > 100 {
		scaledLevel = 100
	}

	// Return the calculated audio level data
	return AudioLevelData{
		Level:    int(scaledLevel),
		Clipping: isClipping,
	}
}

// abs returns the absolute value of a 16-bit integer
func abs(x int16) int16 {
	if x < 0 {
		return -x
	}
	return x
}
