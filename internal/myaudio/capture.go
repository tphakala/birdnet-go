package myaudio

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// AudioDataCallback is a function that can be registered to receive audio data
type AudioDataCallback func(sourceID string, data []byte)

// Global callback registry for broadcasting audio data
var (
	broadcastCallbacks         map[string]AudioDataCallback // Map of sourceID -> callback
	broadcastCallbackMutex     sync.RWMutex
	lastCallbackLogTime        atomic.Int64 // Unix nano timestamp of last active callback log
	lastMissingCallbackLogTime atomic.Int64 // Unix nano timestamp of last missing callback log
)

func init() {
	broadcastCallbacks = make(map[string]AudioDataCallback)
}

// RegisterBroadcastCallback adds a callback function to receive audio data for a specific source
func RegisterBroadcastCallback(sourceID string, callback AudioDataCallback) {
	broadcastCallbackMutex.Lock()
	defer broadcastCallbackMutex.Unlock()
	broadcastCallbacks[sourceID] = callback

	// Get DisplayName for user-friendly logging
	displayName := sourceID // Default to ID if we can't get DisplayName
	if registry := GetRegistry(); registry != nil {
		if source, exists := registry.GetSourceByID(sourceID); exists {
			displayName = source.DisplayName
		}
	}
	log := GetLogger()
	log.Info("registered audio callback",
		logger.String("source", displayName),
		logger.Int("total_callbacks", len(broadcastCallbacks)))
}

// UnregisterBroadcastCallback removes a callback function for a specific source
func UnregisterBroadcastCallback(sourceID string) {
	broadcastCallbackMutex.Lock()
	defer broadcastCallbackMutex.Unlock()
	delete(broadcastCallbacks, sourceID)

	// Get DisplayName for user-friendly logging
	displayName := sourceID // Default to ID if we can't get DisplayName
	if registry := GetRegistry(); registry != nil {
		if source, exists := registry.GetSourceByID(sourceID); exists {
			displayName = source.DisplayName
		}
	}
	log := GetLogger()
	log.Info("unregistered audio callback",
		logger.String("source", displayName),
		logger.Int("remaining_callbacks", len(broadcastCallbacks)))
}

// broadcastAudioData sends audio data to all registered callbacks
func broadcastAudioData(sourceID string, data []byte) {
	broadcastCallbackMutex.RLock()
	callback, exists := broadcastCallbacks[sourceID]

	// Debug log: log registered callbacks less frequently (every 5 minutes)
	// Use atomic operations for thread-safe timestamp access
	lastLogNano := lastCallbackLogTime.Load()
	if time.Since(time.Unix(0, lastLogNano)) > 5*time.Minute {
		// Create a list of registered callback keys with DisplayNames
		registry := GetRegistry()
		keys := make([]string, 0, len(broadcastCallbacks))
		for k := range broadcastCallbacks {
			displayName := k // Default to ID if we can't get DisplayName
			if registry != nil {
				if source, exists := registry.GetSourceByID(k); exists {
					displayName = source.DisplayName
				}
			}
			keys = append(keys, displayName)
		}
		log := GetLogger()
		log.Debug("active audio broadcast callbacks",
			logger.Any("callbacks", keys))
		lastCallbackLogTime.Store(time.Now().UnixNano())
	}

	broadcastCallbackMutex.RUnlock()

	// If no callback registered for this source, skip all processing
	if !exists {
		// Log much less frequently to avoid log spam (once every 5 minutes)
		lastMissingLogNano := lastMissingCallbackLogTime.Load()
		if time.Since(time.Unix(0, lastMissingLogNano)) > 5*time.Minute {
			// Get DisplayName for user-friendly logging
			displayName := sourceID // Default to ID if we can't get DisplayName
			if registry := GetRegistry(); registry != nil {
				if source, exists := registry.GetSourceByID(sourceID); exists {
					displayName = source.DisplayName
				}
			}
			log := GetLogger()
			log.Warn("no broadcast callback registered for source",
				logger.String("source", displayName),
				logger.Int("data_length", len(data)))
			lastMissingCallbackLogTime.Store(time.Now().UnixNano())
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

// AudioLevelData holds audio level data
type AudioLevelData struct {
	Level    int    `json:"level"`    // 0-100
	Clipping bool   `json:"clipping"` // true if clipping is detected
	Source   string `json:"source"`   // Source identifier (e.g., "malgo" for device, or RTSP URL)
	Name     string `json:"name"`     // Human-readable name of the source
}

// UnifiedAudioData holds both audio level and sound level data
type UnifiedAudioData struct {
	// Basic audio level information (always present)
	AudioLevel AudioLevelData `json:"audio_level"`

	// Sound level data (present only when 10-second window is complete)
	SoundLevel *SoundLevelData `json:"sound_level,omitempty"`

	// Metadata
	Timestamp time.Time `json:"timestamp"`
}

// FFmpeg monitoring is now handled by the FFmpegManager in the integration layer
// The FFmpegManager maintains its own internal tracking of active streams

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
			log := GetLogger()
			log.Error("failed to uninitialize context",
				logger.Error(err))
		}
	}()

	// Get a list of capture devices
	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}

	// Pre-allocate slice with capacity for all devices (minus discard devices)
	devices := make([]AudioDeviceInfo, 0, len(infos))

	// Iterate through the list of devices
	for i := range infos {
		// Skip the discard/null device
		if strings.Contains(infos[i].Name(), "Discard all samples") {
			continue
		}

		// Decode the device ID from hexadecimal to ASCII
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			log := GetLogger()
			log.Error("error decoding device ID",
				logger.Int("device_index", i),
				logger.Error(err))
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

// ReconfigureStreams handles dynamic reconfiguration of audio streams
func ReconfigureStreams(settings *conf.Settings, wg *sync.WaitGroup, quitChan, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// Use the FFmpeg manager's sync function to handle all configuration changes
	// This will properly start/stop streams as needed based on the current configuration
	if err := SyncStreamsWithConfig(unifiedAudioChan); err != nil {
		log := GetLogger()
		log.Error("error syncing streams with configuration",
			logger.Error(err))
	}

	// Note: Buffer management is handled by the FFmpegManager via the StartStream/StopStream
	// methods which are called by SyncWithConfig. The activeStreams map is no longer needed
	// as the FFmpegManager maintains its own internal stream tracking.
}

// initializeBuffersForSource handles the initialization of analysis and capture buffers for a given source
func initializeBuffersForSource(sourceID string) error {
	var abExists bool

	// Check if analysis buffer exists using the source ID
	abMutex.RLock()
	_, abExists = analysisBuffers[sourceID]
	abMutex.RUnlock()

	// Initialize analysis buffer if it doesn't exist
	// Pass the ORIGINAL sourceID since AllocateAnalysisBuffer does its own migration
	if !abExists {
		if err := AllocateAnalysisBuffer(conf.BufferSize*3, sourceID); err != nil {
			return fmt.Errorf("failed to initialize analysis buffer: %w", err)
		}
	} else if conf.Setting().Debug {
		log := GetLogger()
		log.Debug("reusing existing analysis buffer",
			logger.String("source_id", sourceID))
	}

	// Check if capture buffer exists using the source ID
	cbMutex.RLock()
	_, cbExists := captureBuffers[sourceID]
	cbMutex.RUnlock()

	// Initialize capture buffer if needed
	// Pass the ORIGINAL sourceID since AllocateCaptureBufferIfNeeded does its own migration
	if !cbExists {
		if err := AllocateCaptureBufferIfNeeded(60, conf.SampleRate, conf.BitDepth/8, sourceID); err != nil {
			// Clean up the analysis buffer if we just created it and capture buffer init fails
			if !abExists {
				if cleanupErr := RemoveAnalysisBuffer(sourceID); cleanupErr != nil {
					log := GetLogger()
					log.Error("failed to cleanup analysis buffer after capture buffer init failure",
						logger.String("source_id", sourceID),
						logger.Error(cleanupErr))
				}
			}
			return fmt.Errorf("failed to initialize capture buffer: %w", err)
		}
	} else if conf.Setting().Debug {
		log := GetLogger()
		log.Debug("reusing existing capture buffer",
			logger.String("source_id", sourceID))
	}

	return nil
}

func CaptureAudio(settings *conf.Settings, wg *sync.WaitGroup, quitChan, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// If no RTSP streams and no audio device configured, return early
	if len(settings.Realtime.RTSP.Streams) == 0 && settings.Realtime.Audio.Source == "" {
		return
	}

	// Initialize RTSP sources - the FFmpegManager will handle buffer initialization
	if len(settings.Realtime.RTSP.Streams) > 0 {
		for _, stream := range settings.Realtime.RTSP.Streams {
			// CaptureAudioRTSP delegates to FFmpegManager which handles everything
			go CaptureAudioRTSP(stream.URL, stream.Transport, wg, quitChan, restartChan, unifiedAudioChan)
		}
	}

	// Handle sound card source if configured
	if settings.Realtime.Audio.Source != "" {
		log := GetLogger()

		// Validate audio device
		if err := ValidateAudioDevice(settings); err != nil {
			log.Warn("audio device validation failed",
				logger.Error(err))
			return
		}

		selectedSource, err := selectCaptureSource(settings)
		if err != nil {
			log.Error("audio device selection failed",
				logger.Error(err))
			return
		}

		// Register the audio source in the registry using the original settings value
		// This ensures consistency with realtime.go registration
		registry := GetRegistry()
		// Guard against nil registry during initialization to prevent panic
		if registry == nil {
			log.Error("registry not available during audio capture initialization, unable to register source")
			return
		}

		source, err := registry.RegisterSource(settings.Realtime.Audio.Source, SourceConfig{
			Type: SourceTypeAudioCard,
		})
		if err != nil {
			log.Error("failed to register audio device source",
				logger.Error(err))
			return
		}

		// Initialize buffers using the registry source ID (UUID-based)
		// This ensures consistency with the AnalysisBufferMonitor
		if err := initializeBuffersForSource(source.ID); err != nil {
			log.Error("failed to initialize buffers for device capture",
				logger.Error(err))
			return
		}

		// Device audio capture - pass source ID for buffer operations
		wg.Go(func() {
			captureAudioMalgo(settings, selectedSource, source.ID, quitChan, restartChan, unifiedAudioChan)
		})
	}
}

// isHardwareDevice checks if the device ID indicates a hardware device
func isHardwareDevice(decodedID string) bool {
	// On Linux, hardware devices have IDs in the format ":X,Y"
	if runtime.GOOS == osLinux {
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
	case osLinux:
		backend = malgo.BackendAlsa
	case osWindows:
		backend = malgo.BackendWasapi
	case osDarwin:
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
	log := GetLogger()

	var backend malgo.Backend
	switch runtime.GOOS {
	case osLinux:
		backend = malgo.BackendAlsa
	case osWindows:
		backend = malgo.BackendWasapi
	case osDarwin:
		backend = malgo.BackendCoreaudio
	}

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, func(message string) {
		if settings.Debug {
			log.Debug("malgo context message", logger.String("message", message))
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

	log.Info("Available capture sources", logger.Int("count", len(infos)))
	for i := range infos {
		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			log.Warn("Error decoding device ID",
				logger.Int("device_index", i),
				logger.Error(err))
			continue
		}

		deviceInfo := infos[i].Name()
		if runtime.GOOS == osLinux {
			deviceInfo = deviceInfo + ", " + decodedID
		}

		if matchesDeviceSettings(decodedID, &infos[i], settings.Realtime.Audio.Source) {
			if TestCaptureDevice(malgoCtx, &infos[i]) {
				log.Info("Audio device selected",
					logger.Int("index", i),
					logger.String("device", deviceInfo))
				return captureSource{
					Name:    infos[i].Name(),
					ID:      decodedID,
					Pointer: infos[i].ID.Pointer(),
				}, nil
			}
			log.Warn("Audio device test failed",
				logger.Int("index", i),
				logger.String("device", deviceInfo))
			continue
		}
		log.Debug("Audio device available",
			logger.Int("index", i),
			logger.String("device", deviceInfo))
	}

	return captureSource{}, fmt.Errorf("no working capture device found matching '%s'", settings.Realtime.Audio.Source)
}

// matchesDeviceSettings checks if the device matches the settings specified by the user.
func matchesDeviceSettings(decodedID string, info *malgo.DeviceInfo, audioSource string) bool {
	// Handle "default" and "sysdefault" on Windows and macOS by selecting the system default device
	if (runtime.GOOS == osWindows || runtime.GOOS == osDarwin) &&
		(audioSource == "sysdefault" || audioSource == "default") {
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
	sourceID string, // Registry source ID for buffer operations
	unifiedAudioChan chan UnifiedAudioData,
) (finalBufferPtr *[]byte, fromPool bool, err error) { // Updated return signature

	log := GetLogger()
	processedSamples := pSamples  // Start with original samples
	needsReturn := false          // Flag if we got something from pool
	currentBufferPtr := &pSamples // Track the buffer source/identity
	var conversionError error

	if formatType != malgo.FormatS16 && formatType != malgo.FormatU8 {
		// Perform conversion, potentially getting a pooled buffer
		var poolUsed bool
		var convertedBufferPtr *[]byte
		convertedBufferPtr, poolUsed, conversionError = ConvertToS16(pSamples, formatType, convertBuffer)
		if conversionError != nil {
			log.Error("error converting audio format", logger.Error(conversionError))
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
			log.Warn("error applying audio EQ filters", logger.Error(eqErr))
			// Non-fatal, just log
		}
	}

	// Write to buffers using source ID (use the safe bufferToUse)
	if writeErr := WriteToAnalysisBuffer(sourceID, bufferToUse); writeErr != nil {
		log.Warn("error writing to analysis buffer", logger.Error(writeErr))
		// Potentially non-fatal, log and continue
	}
	if writeErr := WriteToCaptureBuffer(sourceID, bufferToUse); writeErr != nil {
		log.Warn("error writing to capture buffer", logger.Error(writeErr))
		// Potentially non-fatal, log and continue
	}

	// Broadcast audio data using source ID (use the safe bufferToUse)
	broadcastAudioData(sourceID, bufferToUse)

	// Calculate audio level (use the safe bufferToUse)
	audioLevelData := calculateAudioLevel(bufferToUse, sourceID, source.Name)

	// Create unified audio data structure
	unifiedData := UnifiedAudioData{
		AudioLevel: audioLevelData,
		Timestamp:  time.Now(),
	}

	// Process sound level data if enabled (use the safe bufferToUse) - this may be nil if 10-second window isn't complete
	if conf.Setting().Realtime.Audio.SoundLevel.Enabled {
		if soundLevelData, err := ProcessSoundLevelData(sourceID, bufferToUse); err != nil {
			// Only log actual errors, not normal conditions
			if !errors.Is(err, ErrIntervalIncomplete) && !errors.Is(err, ErrNoAudioData) {
				log.Warn("error processing sound level data",
					logger.Error(err),
					logger.String("source_id", sourceID))
			}
		} else if soundLevelData != nil {
			// Attach sound level data when available
			unifiedData.SoundLevel = soundLevelData
		}
	}

	// Send unified data to channel (non-blocking)
	select {
	case unifiedAudioChan <- unifiedData:
		// Data sent successfully
	default:
		// Channel is full, clear the channel and try again
		for len(unifiedAudioChan) > 0 {
			<-unifiedAudioChan
		}
		select {
		case unifiedAudioChan <- unifiedData:
		default:
			log.Warn("unified audio channel full even after clearing",
				logger.String("source", source.Name))
		}
	}

	return finalBufferPtr, fromPool, nil // Return pointer, pool status, and nil error
}

// handleDeviceStop contains the logic for attempting to restart the audio device
// when it stops unexpectedly.
func handleDeviceStop(captureDevice *malgo.Device, quitChan, restartChan chan struct{}, settings *conf.Settings, restarting *atomic.Int32) {
	log := GetLogger()
	// Ensure the flag is reset when this attempt concludes.
	defer restarting.Store(0)

	select {
	case <-quitChan:
		// Quit signal received, do not restart
		return
	case <-time.After(100 * time.Millisecond):
		// Wait briefly before restarting
		log.Debug("Attempting to restart audio device")
		if err := captureDevice.Start(); err != nil {
			log.Error("failed to restart audio device", logger.Error(err))
			alerting.TryPublish(&alerting.AlertEvent{
				ObjectType: alerting.ObjectTypeDevice,
				EventName:  alerting.EventDeviceError,
				Properties: map[string]any{
					alerting.PropertyError: err.Error(),
				},
			})
			log.Info("attempting full audio context restart in 1 second")
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
		} else {
			log.Debug("Audio device restarted successfully")
			// Flag reset by defer
		}
	}
}

func captureAudioMalgo(settings *conf.Settings, source captureSource, sourceID string, quitChan, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {

	log := GetLogger()

	// Clean up sound level processor when function exits
	defer UnregisterSoundLevelProcessor(sourceID)

	log.Debug("Initializing audio context")

	var backend malgo.Backend
	switch runtime.GOOS {
	case osLinux:
		backend = malgo.BackendAlsa
	case osWindows:
		backend = malgo.BackendWasapi
	case osDarwin:
		backend = malgo.BackendCoreaudio
	}

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, func(message string) {
		if settings.Debug {
			log.Debug("malgo context message", logger.String("message", message))
		}
	})
	if err != nil {
		log.Error("Audio context initialization failed", logger.Error(err))
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
		log.Warn("error initializing filter chain", logger.Error(err))
	}

	// Initialize sound level processor for this source if enabled
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := RegisterSoundLevelProcessor(sourceID, source.Name); err != nil {
			log.Warn("error initializing sound level processor",
				logger.Error(err),
				logger.String("source_id", sourceID),
				logger.String("source_name", source.Name))
		}
	}

	var captureDevice *malgo.Device
	var formatType malgo.FormatType // Declare formatType here
	var scratchBuffer []byte        // Dedicated buffer for conversion destination
	var restarting atomic.Int32     // Flag to prevent concurrent restarts

	onReceiveFrames := func(pSample2, pSamples []byte, framecount uint32) {
		// processAudioFrame now handles pooling internally and returns buffer info
		// Pass scratchBuffer as the potential destination for conversion
		finalBufferPtr, fromPool, err := processAudioFrame(
			pSamples, formatType, scratchBuffer, settings, source, sourceID, unifiedAudioChan,
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
		// NOTE: We are NOT updating scratchBuffer here. It remains independent.
		// If ConvertToS16 consistently needs a larger buffer than scratchBuffer provides,
		// it will keep allocating, which is less optimal but safe.
		// A more advanced optimization could resize scratchBuffer based on ConvertToS16 feedback.
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
		log.Error("Device initialization failed", logger.Error(err))
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeDevice,
			EventName:  alerting.EventDeviceError,
			Properties: map[string]any{
				alerting.PropertyDeviceName: source.Name,
				alerting.PropertyError:      err.Error(),
			},
		})
		conf.PrintUserInfo()
		return
	}

	// Get the actual format of the capture device
	formatType = captureDevice.CaptureFormat()

	// Log device info if in debug mode
	if settings.Debug {
		logDeviceInfo(log, captureDevice, formatType)
	}

	log.Debug("Starting audio device")
	err = captureDevice.Start()
	if err != nil {
		log.Error("Device start failed", logger.Error(err))
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeDevice,
			EventName:  alerting.EventDeviceError,
			Properties: map[string]any{
				alerting.PropertyDeviceName: source.Name,
				alerting.PropertyError:      err.Error(),
			},
		})
		return
	}
	defer captureDevice.Stop() //nolint:errcheck // We handle errors in the caller

	log.Info("Listening on audio source",
		logger.String("name", source.Name),
		logger.String("id", source.ID))

	// Publish device started alert event
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeDevice,
		EventName:  alerting.EventDeviceStarted,
		Properties: map[string]any{
			alerting.PropertyDeviceName: source.Name,
		},
	})

	// Loop until quit or restart signal
	for {
		select {
		case <-quitChan:
			log.Info("Stopping audio capture due to quit signal")
			alerting.TryPublish(&alerting.AlertEvent{
				ObjectType: alerting.ObjectTypeDevice,
				EventName:  alerting.EventDeviceStopped,
				Properties: map[string]any{
					alerting.PropertyDeviceName: source.Name,
				},
			})
			time.Sleep(100 * time.Millisecond) // Allow Stop() to execute
			return
		case <-restartChan:
			log.Debug("Restarting audio capture")
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// logDeviceInfo logs detailed information about the initialized capture device.
func logDeviceInfo(log logger.Logger, dev *malgo.Device, format malgo.FormatType) {
	var bitDepth int
	switch format {
	case malgo.FormatU8:
		bitDepth = 8
	case malgo.FormatS16:
		bitDepth = 16
	case malgo.FormatS24:
		bitDepth = 24
	case malgo.FormatS32, malgo.FormatF32:
		bitDepth = 32
	default:
		bitDepth = 0 // Unknown
	}
	log.Debug("Initialized capture device",
		logger.Int("format_type", int(format)),
		logger.Int("bit_depth", bitDepth),
		logger.Int("channels", int(dev.CaptureChannels())),
		logger.Int("sample_rate", int(dev.SampleRate())))
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
		sample := int16(binary.LittleEndian.Uint16(samples[i : i+2])) //nolint:gosec // G115: audio sample conversion within 16-bit range
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

// Pool of fixed-size byte slices to avoid frequent allocations
var s16BufferPool = sync.Pool{
	New: func() any {
		// Pre-allocate buffers for typical frame size (2048 bytes for malgo / 1024 16-bit samples)
		buffer := make([]byte, 2048)
		return &buffer // Return a pointer to avoid allocations
	},
}

// ConvertToS16WithBuffer converts audio samples from higher bit depths to 16-bit format
// using a caller-provided or pooled buffer to minimize allocations.
// This version is optimized for real-time processing with fixed-size frames.
//
// Parameters:
//   - samples: Input audio samples
//   - sourceFormat: Source format (malgo.FormatS24, malgo.FormatS32, malgo.FormatF32)
//   - outputBuffer: Optional pre-allocated output buffer (pass nil to use pool)
//
// Returns:
//   - outputBufferPtr: A pointer to the slice of the output buffer containing the converted data
//   - fromPool: Boolean indicating if the output buffer is from the pool
//   - err: Error if conversion fails
func ConvertToS16(samples []byte, sourceFormat malgo.FormatType, outputBuffer []byte) (outputBufferPtr *[]byte, fromPool bool, err error) {
	if len(samples) == 0 {
		empty := []byte{}
		return &empty, false, nil
	}

	var bytesPerSample int
	switch sourceFormat {
	case malgo.FormatS24:
		bytesPerSample = 3
	case malgo.FormatS32, malgo.FormatF32:
		bytesPerSample = 4
	default:
		return nil, false, fmt.Errorf("unsupported source format: %v", sourceFormat)
	}

	// Ensure we have complete samples
	validSampleCount := len(samples) / bytesPerSample
	if validSampleCount == 0 {
		empty := []byte{}
		return &empty, false, nil
	}

	// Calculate required output size
	requiredSize := validSampleCount * 2 // 2 bytes per sample for 16-bit

	// Use provided buffer, pool buffer, or allocate new one based on outputBuffer status
	switch {
	case outputBuffer == nil:
		// No buffer provided, get from pool or allocate new
		if requiredSize <= 2048 {
			outputBufferPtr = s16BufferPool.Get().(*[]byte)
			outputBuffer = *outputBufferPtr // Dereference for internal use
			fromPool = true
		} else {
			newBuffer := make([]byte, requiredSize)
			outputBuffer = newBuffer
			outputBufferPtr = &newBuffer
		}
	case len(outputBuffer) < requiredSize:
		// Provided buffer is too small
		err = fmt.Errorf("output buffer too small: need %d bytes, got %d",
			requiredSize, len(outputBuffer))
		return // Return named values
	default:
		// Use the provided buffer
		outputBufferPtr = &outputBuffer
	}

	// Convert samples into the dereferenced buffer
	actualOutputBuffer := *outputBufferPtr
	for i := range validSampleCount {
		srcIdx := i * bytesPerSample
		dstIdx := i * 2

		switch sourceFormat {
		case malgo.FormatS24:
			// Convert 24-bit (3 bytes) to 16-bit
			val := int32(samples[srcIdx]) | int32(samples[srcIdx+1])<<8 | int32(samples[srcIdx+2])<<16
			// Sign extend if the most significant bit is set
			if (val & 0x800000) != 0 {
				val |= int32(-0x1000000)
			}
			// Shift right by 8 bits to get a 16-bit value
			val >>= 8
			// Clamp to 16-bit range
			if val > 32767 {
				val = 32767
			} else if val < -32768 {
				val = -32768
			}
			binary.LittleEndian.PutUint16(actualOutputBuffer[dstIdx:dstIdx+2], uint16(val)) //nolint:gosec // G115: val clamped to 16-bit range above

		case malgo.FormatS32:
			// Convert 32-bit integer to 16-bit
			val := int32(binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+4])) //nolint:gosec // G115: 32-bit to 32-bit conversion
			val >>= 16
			// Clamp to 16-bit range
			if val > 32767 {
				val = 32767
			} else if val < -32768 {
				val = -32768
			}
			binary.LittleEndian.PutUint16(actualOutputBuffer[dstIdx:dstIdx+2], uint16(val)) //nolint:gosec // G115: val clamped to 16-bit range above

		case malgo.FormatF32:
			// Convert 32-bit float to 16-bit integer
			bits := binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+4])
			val := math.Float32frombits(bits)
			// Scale float [-1.0, 1.0] to 16-bit integer range
			val *= 32767.0
			// Clamp to 16-bit range
			if val > 32767.0 {
				val = 32767.0
			} else if val < -32768.0 {
				val = -32768.0
			}
			binary.LittleEndian.PutUint16(actualOutputBuffer[dstIdx:dstIdx+2], uint16(int16(val))) //nolint:gosec // G115: val clamped to 16-bit range above
		case malgo.FormatUnknown, malgo.FormatU8, malgo.FormatS16:
			// These formats are not handled by this conversion function
			// The caller should ensure only S24, S32, or F32 formats are passed
		}
	}

	// Adjust the slice length of the buffer pointed to
	*outputBufferPtr = (*outputBufferPtr)[:requiredSize]

	// Return the pointer to the buffer (err is nil by default)
	return
}

// ReturnBufferToPool returns a buffer pointer to the pool if it came from the pool
func ReturnBufferToPool(bufferPtr *[]byte, fromPool bool) {
	if fromPool && bufferPtr != nil && *bufferPtr != nil {
		// Reset slice length to its capacity before returning to pool
		full := (*bufferPtr)[:cap(*bufferPtr)]
		s16BufferPool.Put(&full)
	}
}
