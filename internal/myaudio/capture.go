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
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/fatih/color"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/malgo"
)

// AudioDataCallback is a function that can be registered to receive audio data
type AudioDataCallback func(sourceID string, data []byte)

// Global callback registry for broadcasting audio data
var (
	broadcastCallbacks         map[string]AudioDataCallback // Map of sourceID -> callback
	broadcastCallbackMutex     sync.RWMutex
	lastCallbackLogTime        time.Time // Last time we logged active callbacks
	lastMissingCallbackLogTime time.Time // Last time we logged missing callbacks
)

func init() {
	broadcastCallbacks = make(map[string]AudioDataCallback)
}

// RegisterBroadcastCallback adds a callback function to receive audio data for a specific source
func RegisterBroadcastCallback(sourceID string, callback AudioDataCallback) {
	broadcastCallbackMutex.Lock()
	defer broadcastCallbackMutex.Unlock()
	broadcastCallbacks[sourceID] = callback
	log.Printf("🎧 Registered audio callback for source: %s, total callbacks: %d",
		sourceID, len(broadcastCallbacks))
}

// UnregisterBroadcastCallback removes a callback function for a specific source
func UnregisterBroadcastCallback(sourceID string) {
	broadcastCallbackMutex.Lock()
	defer broadcastCallbackMutex.Unlock()
	delete(broadcastCallbacks, sourceID)
	log.Printf("🎧 Unregistered audio callback for source: %s, remaining callbacks: %d",
		sourceID, len(broadcastCallbacks))
}

// broadcastAudioData sends audio data to all registered callbacks
func broadcastAudioData(sourceID string, data []byte) {
	broadcastCallbackMutex.RLock()
	callback, exists := broadcastCallbacks[sourceID]

	// Debug log: log registered callbacks less frequently (every 5 minutes)
	// Use a proper timestamp comparison instead of modulus which can cause spurious logging
	if time.Since(lastCallbackLogTime) > 5*time.Minute {
		// Create a list of registered callback keys to show what sources are registered
		var keys []string
		for k := range broadcastCallbacks {
			keys = append(keys, k)
		}
		log.Printf("🔊 Active audio broadcast callbacks: %v", keys)
		lastCallbackLogTime = time.Now()
	}

	broadcastCallbackMutex.RUnlock()

	// If no callback registered for this source, skip all processing
	if !exists {
		// Log much less frequently to avoid log spam (once every 5 minutes)
		if time.Since(lastMissingCallbackLogTime) > 5*time.Minute {
			log.Printf("⚠️ No broadcast callback registered for source: %s, data length: %d bytes",
				sourceID, len(data))
			lastMissingCallbackLogTime = time.Now()
		}
		return
	}

	// Call the callback for this source
	callback(sourceID, data)
}

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

// activeStreams keeps track of currently active RTSP streams
var activeStreams sync.Map

// ffmpegMonitor is the global FFmpeg process monitor
var ffmpegMonitor *FFmpegMonitor

// ListAudioSources returns a list of available audio capture devices.
func ListAudioSources() ([]AudioDeviceInfo, error) {
	// Create a slice to store audio device information
	var devices []AudioDeviceInfo

	// Initialize the audio context
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return devices, fmt.Errorf("failed to initialize context: %w", err)
	}

	// Ensure the context is uninitialized when the function returns
	defer func() {
		if err := ctx.Uninit(); err != nil {
			log.Printf("❌ failed to uninitialize context: %v", err)
		}
	}()

	// Get a list of capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return devices, fmt.Errorf("failed to get devices: %w", err)
	}

	// Iterate through the list of devices
	for i := range infos {
		// Skip the discard/null device
		if strings.Contains(infos[i].Name(), "Discard all samples") {
			continue
		}

		// Decode the device ID from hexadecimal to ASCII
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			log.Printf("❌ Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		// Add the device information to the devices slice
		devices = append(devices, AudioDeviceInfo{
			Index: i,
			Name:  infos[i].Name(),
			ID:    decodedID,
		})
	}

	// Return the list of devices and nil error
	return devices, nil
}

// ReconfigureRTSPStreams handles dynamic reconfiguration of RTSP streams
func ReconfigureRTSPStreams(settings *conf.Settings, wg *sync.WaitGroup, quitChan, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	// If there are no RTSP URLs configured and FFmpeg monitor is running, stop it
	if len(settings.Realtime.RTSP.URLs) == 0 {
		if ffmpegMonitor != nil {
			ffmpegMonitor.Stop()
			ffmpegMonitor = nil
		}
		return
	}

	// Initialize FFmpeg monitor if not already running
	if ffmpegMonitor == nil {
		ffmpegMonitor = NewDefaultFFmpegMonitor()
		ffmpegMonitor.Start()
	}

	// Get current active streams
	currentStreams := make(map[string]bool)
	activeStreams.Range(func(key, value interface{}) bool {
		currentStreams[key.(string)] = true
		return true
	})

	// Stop streams that are no longer in settings
	for url := range currentStreams {
		found := false
		for _, newURL := range settings.Realtime.RTSP.URLs {
			if url == newURL {
				found = true
				break
			}
		}
		if !found {
			// Stream is no longer in settings, stop it
			if process, exists := ffmpegProcesses.Load(url); exists {
				if p, ok := process.(*FFmpegProcess); ok {
					// Stop the FFmpeg process first
					p.Cleanup(url)
					// Wait a short time for the process to fully stop
					time.Sleep(100 * time.Millisecond)
				}
			}

			// Mark stream as inactive before removing buffers
			activeStreams.Delete(url)
			log.Printf("⬇️ Stream %s removed", url)
			// Wait a short time for any in-flight writes to complete
			time.Sleep(100 * time.Millisecond)

			// Now it's safe to remove the buffers
			if err := RemoveAnalysisBuffer(url); err != nil {
				log.Printf("❌ Warning: failed to remove analysis buffer for %s: %v", url, err)
			}
			if err := RemoveCaptureBuffer(url); err != nil {
				log.Printf("❌ Warning: failed to remove capture buffer for %s: %v", url, err)
			}
		}
	}

	// Start new streams
	for _, url := range settings.Realtime.RTSP.URLs {
		// Check if stream is already active
		if _, exists := activeStreams.Load(url); exists {
			continue
		}

		abExists, cbExists := false, false //nolint:wastedassign // Need to initialize variables
		// Check if analysis buffer exists
		abMutex.RLock()
		_, abExists = analysisBuffers[url]
		abMutex.RUnlock()

		// Check if capture buffer exists
		cbMutex.RLock()
		_, cbExists = captureBuffers[url]
		cbMutex.RUnlock()

		// Initialize analysis buffer if it doesn't exist
		if !abExists {
			if err := AllocateAnalysisBuffer(conf.BufferSize*3, url); err != nil {
				log.Printf("❌ Failed to initialize analysis buffer for %s: %v", url, err)
				continue
			}
		}

		// Initialize capture buffer if it doesn't exist
		if !cbExists {
			if err := AllocateCaptureBuffer(60, conf.SampleRate, conf.BitDepth/8, url); err != nil {
				// Clean up the ring buffer if audio buffer init fails and we just created it
				if !abExists {
					err := RemoveCaptureBuffer(url)
					if err != nil {
						log.Printf("❌ Failed to remove capture buffer for %s: %v", url, err)
					}
				}
				log.Printf("❌ Failed to initialize capture buffer for %s: %v", url, err)
				continue
			}
		}

		// New stream, start it
		activeStreams.Store(url, true)
		wg.Add(1)
		go CaptureAudioRTSP(url, settings.Realtime.RTSP.Transport, wg, quitChan, restartChan, audioLevelChan)
	}
}

// initializeBuffersForSource handles the initialization of analysis and capture buffers for a given source
func initializeBuffersForSource(sourceID string) error {
	var abExists, cbExists bool

	// Check if analysis buffer exists
	abMutex.RLock()
	_, abExists = analysisBuffers[sourceID]
	abMutex.RUnlock()

	// Check if capture buffer exists
	cbMutex.RLock()
	_, cbExists = captureBuffers[sourceID]
	cbMutex.RUnlock()

	// Initialize analysis buffer if it doesn't exist
	if !abExists {
		if err := AllocateAnalysisBuffer(conf.BufferSize*3, sourceID); err != nil {
			return fmt.Errorf("failed to initialize analysis buffer: %w", err)
		}
	}

	// Initialize capture buffer if it doesn't exist
	if !cbExists {
		if err := AllocateCaptureBuffer(60, conf.SampleRate, conf.BitDepth/8, sourceID); err != nil {
			// Clean up the analysis buffer if we just created it and capture buffer init fails
			if !abExists {
				if cleanupErr := RemoveAnalysisBuffer(sourceID); cleanupErr != nil {
					log.Printf("❌ Failed to cleanup analysis buffer after capture buffer init failure for %s: %v", sourceID, cleanupErr)
				}
			}
			return fmt.Errorf("failed to initialize capture buffer: %w", err)
		}
	}

	return nil
}

func CaptureAudio(settings *conf.Settings, wg *sync.WaitGroup, quitChan, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	// If no RTSP URLs and no audio device configured, return early
	if len(settings.Realtime.RTSP.URLs) == 0 && settings.Realtime.Audio.Source == "" {
		return
	}

	// Initialize buffers for RTSP sources
	if len(settings.Realtime.RTSP.URLs) > 0 {
		for _, url := range settings.Realtime.RTSP.URLs {
			if err := initializeBuffersForSource(url); err != nil {
				log.Printf("❌ Failed to initialize buffers for RTSP source %s: %v", url, err)
				continue
			}

			activeStreams.Store(url, true)
			wg.Add(1)
			go CaptureAudioRTSP(url, settings.Realtime.RTSP.Transport, wg, quitChan, restartChan, audioLevelChan)
		}
	}

	// Handle sound card source if configured
	if settings.Realtime.Audio.Source != "" {
		// Validate audio device
		if err := ValidateAudioDevice(settings); err != nil {
			log.Printf("⚠️ Audio device validation failed: %v", err)
			return
		}

		selectedSource, err := selectCaptureSource(settings)
		if err != nil {
			log.Printf("❌ Audio device selection failed: %v", err)
			return
		}

		// Initialize buffers for local audio device
		if err := initializeBuffersForSource("malgo"); err != nil {
			log.Printf("❌ Failed to initialize buffers for device capture: %v", err)
			return
		}

		// Device audio capture
		go captureAudioMalgo(settings, selectedSource, wg, quitChan, restartChan, audioLevelChan)
	}
}

// isHardwareDevice checks if the device ID indicates a hardware device
func isHardwareDevice(decodedID string) bool {
	// On Linux, hardware devices have IDs in the format ":X,Y"
	if runtime.GOOS == "linux" {
		return strings.Contains(decodedID, ":") && strings.Contains(decodedID, ",")
	}
	// On Windows and macOS, consider all devices as potential hardware devices
	// as the ID format is different and we rely on the OS's device enumeration
	return true
}

// getHardwareDevices filters the device infos to return only hardware devices
func getHardwareDevices(infos []malgo.DeviceInfo) []malgo.DeviceInfo {
	var hardwareDevices []malgo.DeviceInfo
	for i := range infos {
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			continue
		}
		if isHardwareDevice(decodedID) {
			hardwareDevices = append(hardwareDevices, infos[i])
		}
	}
	return hardwareDevices
}

// TestCaptureDevice tests if a capture device can be initialized and started.
// Returns true if the device is working, false otherwise.
func TestCaptureDevice(ctx *malgo.AllocatedContext, info *malgo.DeviceInfo) bool {
	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	// Malgo bit depth conversion seems to be broken, so we'll do it manually,
	// accept default format from capture device
	//deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.Capture.DeviceID = info.ID.Pointer()
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1

	// Try to initialize the device
	device, err := malgo.InitDevice(ctx.Context, deviceConfig, malgo.DeviceCallbacks{})
	if err != nil {
		return false
	}
	defer device.Uninit()

	// Try to start the device
	if err := device.Start(); err != nil {
		return false
	}

	// Stop the device
	_ = device.Stop()
	return true
}

// ValidateAudioDevice checks if the configured audio source is available and working.
// Returns an error if the device is not available or not working.
// This function also updates the settings if the device is not valid.
func ValidateAudioDevice(settings *conf.Settings) error {
	if settings.Realtime.Audio.Source == "" {
		return nil
	}

	var backend malgo.Backend
	switch runtime.GOOS {
	case "linux":
		backend = malgo.BackendAlsa
	case "windows":
		backend = malgo.BackendWasapi
	case "darwin":
		backend = malgo.BackendCoreaudio
	}

	// Initialize malgo context
	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		settings.Realtime.Audio.Source = ""
		return fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer malgoCtx.Uninit() //nolint:errcheck // We handle errors in the caller

	// Get list of capture devices
	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		settings.Realtime.Audio.Source = ""
		return fmt.Errorf("failed to get capture devices: %w", err)
	}

	// Filter to get only hardware devices to check if any are available
	hardwareDevices := getHardwareDevices(infos)
	if len(hardwareDevices) == 0 {
		settings.Realtime.Audio.Source = ""
		return fmt.Errorf("no hardware audio capture devices found")
	}

	// Try to find and test the configured device, in this we also accept alsa speudo devices
	for i := range infos {
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			continue
		}

		if matchesDeviceSettings(decodedID, &infos[i], settings.Realtime.Audio.Source) {
			if TestCaptureDevice(malgoCtx, &infos[i]) {
				return nil
			}
			settings.Realtime.Audio.Source = ""
			return fmt.Errorf("configured audio device '%s' failed hardware test", settings.Realtime.Audio.Source)
		}
	}

	//settings.Realtime.Audio.Source = ""
	return fmt.Errorf("configured audio device '%s' not found", settings.Realtime.Audio.Source)
}

// selectCaptureSource selects and tests an appropriate capture device based on the provided settings.
func selectCaptureSource(settings *conf.Settings) (captureSource, error) {
	var backend malgo.Backend
	switch runtime.GOOS {
	case "linux":
		backend = malgo.BackendAlsa
	case "windows":
		backend = malgo.BackendWasapi
	case "darwin":
		backend = malgo.BackendCoreaudio
	}

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, func(message string) {
		if settings.Debug {
			fmt.Print(message)
		}
	})
	if err != nil {
		return captureSource{}, fmt.Errorf("audio context initialization failed: %w", err)
	}
	defer malgoCtx.Uninit() //nolint:errcheck // We handle errors in the caller

	// Get list of capture sources
	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		return captureSource{}, fmt.Errorf("failed to get capture devices: %w", err)
	}

	fmt.Println("Available Capture Sources:")
	for i := range infos {
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			fmt.Printf("❌ Error decoding ID for device %d: %v\n", i, err)
			continue
		}

		output := fmt.Sprintf("  %d: %s", i, infos[i].Name())
		if runtime.GOOS == "linux" {
			output = fmt.Sprintf("%s, %s", output, decodedID)
		}

		if matchesDeviceSettings(decodedID, &infos[i], settings.Realtime.Audio.Source) {
			if TestCaptureDevice(malgoCtx, &infos[i]) {
				fmt.Printf("%s (✅ selected)\n", output)
				return captureSource{
					Name:    infos[i].Name(),
					ID:      decodedID,
					Pointer: infos[i].ID.Pointer(),
				}, nil
			}
			fmt.Printf("%s (❌ device test failed)\n", output)
			continue
		}
		fmt.Println(output)
	}

	return captureSource{}, fmt.Errorf("no working capture device found matching '%s'", settings.Realtime.Audio.Source)
}

// matchesDeviceSettings checks if the device matches the settings specified by the user.
func matchesDeviceSettings(decodedID string, info *malgo.DeviceInfo, audioSource string) bool {
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

// processAudioFrame handles the processing of a single audio frame received from malgo.
// It performs format conversion, applies EQ, writes to buffers, broadcasts, calculates levels,
// and handles buffer safety for shared/pooled buffers.
// Returns:
//   - *[]byte: Pointer to the final processed buffer (could be original, pooled, or new)
//   - bool: True if the returned buffer pointer points to a buffer from the pool
//   - error: If a critical step fails
func processAudioFrame(
	pSamples []byte,
	formatType malgo.FormatType,
	convertBuffer []byte, // Can be nil, used if provided
	settings *conf.Settings,
	source captureSource,
	audioLevelChan chan AudioLevelData,
) (finalBufferPtr *[]byte, fromPool bool, err error) { // Updated return signature

	processedSamples := pSamples             // Start with original samples
	needsReturn := false                     // Flag if we got something from pool
	var currentBufferPtr *[]byte = &pSamples // Track the buffer source/identity
	var conversionError error

	if formatType != malgo.FormatS16 && formatType != malgo.FormatU8 {
		// Perform conversion, potentially getting a pooled buffer
		var poolUsed bool
		var convertedBufferPtr *[]byte
		convertedBufferPtr, poolUsed, conversionError = ConvertToS16(pSamples, formatType, convertBuffer)
		if conversionError != nil {
			log.Printf("❌ Error converting audio format: %v", conversionError)
			return nil, false, conversionError // Return the specific error
		}
		// Update to use the converted data
		processedSamples = *convertedBufferPtr
		currentBufferPtr = convertedBufferPtr // Track the potentially pooled or provided/new buffer
		needsReturn = poolUsed
	}

	// --- Buffer Safety Handling ---
	var bufferToUse []byte
	// Check if currentBufferPtr points to the same underlying data as pSamples
	isOriginalPSamples := (len(processedSamples) > 0 && len(pSamples) > 0 && &processedSamples[0] == &pSamples[0]) || (len(processedSamples) == 0 && len(pSamples) == 0)
	finalBufferPtr = currentBufferPtr // Assume we use the current buffer initially
	fromPool = needsReturn            // Pass along pool status
	var safeCopyPtr *[]byte           // To hold pointer if we get from pool for copying

	switch {
	case needsReturn:
		// Buffer came from the pool (currentBufferPtr) - MUST copy for safety
		safeCopyPtr = s16BufferPool.Get().(*[]byte)        // Get a fresh buffer for the copy
		safeCopy := (*safeCopyPtr)[:len(processedSamples)] // Slice it to the needed length
		copy(safeCopy, processedSamples)                   // Copy the data
		bufferToUse = safeCopy                             // This is the safe buffer to use downstream

		// Return the original pooled buffer (pointed to by currentBufferPtr) *now*
		ReturnBufferToPool(currentBufferPtr, needsReturn)

		// Update finalBufferPtr to point to the *new* pooled buffer holding the safe copy
		finalBufferPtr = safeCopyPtr
		fromPool = true // The final buffer IS now considered pooled

	case isOriginalPSamples:
		// Using the original pSamples buffer directly - MUST copy for safety
		safeCopyPtr = s16BufferPool.Get().(*[]byte)        // Get a buffer for the copy
		safeCopy := (*safeCopyPtr)[:len(processedSamples)] // Slice it
		copy(safeCopy, processedSamples)                   // Copy data
		bufferToUse = safeCopy                             // Use the copy

		// Update finalBufferPtr to point to the pooled buffer holding the safe copy
		finalBufferPtr = safeCopyPtr
		fromPool = true // The final buffer IS now considered pooled

	default:
		// Buffer was newly allocated or provided (not pooled, not pSamples) - Safe to use directly
		bufferToUse = processedSamples
		// finalBufferPtr already points to this buffer (currentBufferPtr)
		// fromPool is already false
	}
	// --- End Buffer Safety Handling ---

	// Apply audio EQ filters if enabled (use the safe bufferToUse)
	if settings.Realtime.Audio.Equalizer.Enabled {
		if eqErr := ApplyFilters(bufferToUse); eqErr != nil {
			log.Printf("❌ Error applying audio EQ filters: %v", eqErr)
			// Non-fatal, just log
		}
	}

	// Write to buffers (use the safe bufferToUse)
	if writeErr := WriteToAnalysisBuffer("malgo", bufferToUse); writeErr != nil {
		log.Printf("❌ Error writing to analysis buffer: %v", writeErr)
		// Potentially non-fatal, log and continue
	}
	if writeErr := WriteToCaptureBuffer("malgo", bufferToUse); writeErr != nil {
		log.Printf("❌ Error writing to capture buffer: %v", writeErr)
		// Potentially non-fatal, log and continue
	}

	// Broadcast audio data (use the safe bufferToUse)
	broadcastAudioData("malgo", bufferToUse)

	// Process audio level data
	ProcessAudioLevel(pSamples, "malgo", source.Name, audioLevelChan)

	return finalBufferPtr, fromPool, nil // Return pointer, pool status, and nil error
}

// handleDeviceStop contains the logic for attempting to restart the audio device
// when it stops unexpectedly.
func handleDeviceStop(captureDevice *malgo.Device, quitChan, restartChan chan struct{}, settings *conf.Settings, restarting *atomic.Int32) {
	// Ensure the flag is reset when this attempt concludes.
	defer restarting.Store(0)

	select {
	case <-quitChan:
		// Quit signal received, do not restart
		return
	case <-time.After(100 * time.Millisecond):
		// Wait briefly before restarting
		if settings.Debug {
			fmt.Println("🔄 Attempting to restart audio device.")
		}
		if err := captureDevice.Start(); err != nil {
			log.Printf("❌ Failed to restart audio device: %v", err)
			log.Println("🔄 Attempting full audio context restart in 1 second.")
			time.Sleep(1 * time.Second)
			// Before sending the signal, check if we are already quitting.
			select {
			case <-quitChan:
				return // Don't send restart if quitting
			default:
				// Try sending the restart signal, but don't block if quitChan closes.
				select {
				case restartChan <- struct{}{}:
				case <-quitChan:
				}
			}
		} else if settings.Debug {
			fmt.Println("🔄 Audio device restarted successfully.")
			// Flag reset by defer
		}
	}
}

func captureAudioMalgo(settings *conf.Settings, source captureSource, wg *sync.WaitGroup, quitChan, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	wg.Add(1)
	defer wg.Done()

	if settings.Debug {
		fmt.Println("Initializing context")
	}

	var backend malgo.Backend
	switch runtime.GOOS {
	case "linux":
		backend = malgo.BackendAlsa
	case "windows":
		backend = malgo.BackendWasapi
	case "darwin":
		backend = malgo.BackendCoreaudio
	}

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, func(message string) {
		if settings.Debug {
			fmt.Print(message)
		}
	})
	if err != nil {
		color.New(color.FgHiYellow).Fprintln(os.Stderr, "❌ context init failed:", err)
		return
	}
	defer malgoCtx.Uninit() //nolint:errcheck // We handle errors in the caller

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	// deviceConfig.Capture.Format = malgo.FormatS16 // Let malgo choose or use default
	deviceConfig.Capture.Channels = conf.NumChannels
	deviceConfig.SampleRate = conf.SampleRate
	deviceConfig.Alsa.NoMMap = 1
	deviceConfig.Capture.DeviceID = source.Pointer

	// Initialize the filter chain
	if err := InitializeFilterChain(settings); err != nil {
		log.Printf("❌ Error initializing filter chain: %v", err)
	}

	var captureDevice *malgo.Device
	var formatType malgo.FormatType // Declare formatType here
	var scratchBuffer []byte        // Dedicated buffer for conversion destination
	var restarting atomic.Int32     // Flag to prevent concurrent restarts

	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		// processAudioFrame now handles pooling internally and returns buffer info
		// Pass scratchBuffer as the potential destination for conversion
		finalBufferPtr, fromPool, err := processAudioFrame(
			pSamples, formatType, scratchBuffer, settings, source, audioLevelChan,
		)
		if err != nil {
			// Error already logged in processAudioFrame
			return
		}

		// If the buffer came from the pool (either originally or as a safeCopy),
		// return it now that processing for this frame is done.
		if fromPool && finalBufferPtr != nil {
			ReturnBufferToPool(finalBufferPtr, fromPool)
		}

		// Process audio level data
		ProcessAudioLevel(pSamples, "malgo", source.Name, audioLevelChan)
	}

	// onStopDevice logic is now in handleDeviceStop, guarded by atomic flag
	onStopDevice := func() {
		if restarting.CompareAndSwap(0, 1) {
			go handleDeviceStop(captureDevice, quitChan, restartChan, settings, &restarting)
		}
	}

	// Device callback to assign function to call when audio data is received
	deviceCallbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
		Stop: onStopDevice,
	}

	// Initialize the capture device
	captureDevice, err = malgo.InitDevice(malgoCtx.Context, deviceConfig, deviceCallbacks)
	if err != nil {
		color.New(color.FgHiYellow).Fprintln(os.Stderr, "❌ Device initialization failed:", err)
		conf.PrintUserInfo()
		return
	}

	// Get the actual format of the capture device
	formatType = captureDevice.CaptureFormat()

	// Print device info if in debug mode
	if settings.Debug {
		printDeviceInfo(captureDevice, formatType)
	}

	if settings.Debug {
		fmt.Println("Starting device")
	}
	err = captureDevice.Start()
	if err != nil {
		color.New(color.FgHiYellow).Fprintln(os.Stderr, "❌ Device start failed:", err)
		return
	}
	defer captureDevice.Stop() //nolint:errcheck // We handle errors in the caller

	if settings.Debug {
		fmt.Println("Device started")
	}
	// print audio device we are attached to
	color.New(color.FgHiGreen).Printf("Listening on source: %s (%s)\n", source.Name, source.ID)

	// Set up cleanup ticker to periodically clean up audio level trackers
	cleanupTicker := time.NewTicker(10 * time.Minute)
	defer cleanupTicker.Stop()

	// Now, instead of directly waiting on QuitChannel,
	// check if it's closed in a non-blocking select.
	// This loop will keep running until QuitChannel is closed.
	for {
		select {
		case <-quitChan:
			fmt.Println("🛑 Stopping audio capture due to quit signal.")
			time.Sleep(100 * time.Millisecond) // Allow Stop() to execute
			return
		case <-restartChan:
			if settings.Debug {
				fmt.Println("🔄 Restarting audio capture.")
			}
			return
		case <-cleanupTicker.C:
			// Clean up audio level trackers
			CleanupAudioLevelTrackers()
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// calculateAudioLevel calculates the RMS (Root Mean Square) of the audio samples
// and returns an AudioLevelData struct with the level and clipping status
func calculateAudioLevel(samples []byte, source, name string) AudioLevelData {
	// If there are no samples, return zero level and no clipping
	if len(samples) == 0 {
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
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
		return AudioLevelData{Level: 0, Clipping: false, Source: source, Name: name}
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
		Source:   source,
		Name:     name,
	}
}
