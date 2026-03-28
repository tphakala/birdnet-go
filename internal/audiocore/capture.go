// Package audiocore provides the core audio infrastructure for BirdNET-Go.
// capture.go — per-device malgo capture initialisation and callback.
package audiocore

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Platform OS name constants used for backend selection.
const (
	captureOSLinux   = "linux"
	captureOSWindows = "windows"
	captureOSDarwin  = "darwin"
)

// Device identifier tokens used to match platform default devices.
// On Windows and macOS, these select the system default capture device
// (IsDefault == 1). On Linux/ALSA, "sysdefault" and "default" are real
// ALSA PCM device names resolved by the ALSA configuration, so they are
// matched by substring against the device name or exact match against the
// decoded device ID (handled by the general matching path).
const (
	// DeviceIDSysDefault is the ALSA pseudo-device that routes to the
	// kernel-configured default sound card.
	DeviceIDSysDefault = "sysdefault"

	// DeviceIDDefault is the ALSA/PulseAudio default device, or the
	// platform default on Windows (WASAPI) and macOS (CoreAudio).
	DeviceIDDefault = "default"
)

// platformBackend returns the preferred malgo backend for the current OS.
func platformBackend() malgo.Backend {
	switch runtime.GOOS {
	case captureOSLinux:
		return malgo.BackendAlsa
	case captureOSWindows:
		return malgo.BackendWasapi
	case captureOSDarwin:
		return malgo.BackendCoreaudio
	default:
		return malgo.BackendNull
	}
}

// hexToASCII converts a hexadecimal string (as returned by malgo's
// DeviceID.String()) to its ASCII representation.
func hexToASCII(hexStr string) (string, error) {
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// listDevices enumerates capture devices on the host using malgo.
// It skips null/discard devices and deduplicates by name (ALSA pseudo-devices).
//
// malgo cleanup: InitContext allocates a C ma_context via ma_malloc. The caller
// must call Uninit() (ma_context_uninit) to release backend resources, then
// Free() (ma_free) to release the C heap allocation. Device.Uninit() handles
// both steps internally, but AllocatedContext requires the two-step teardown.
func listDevices(log logger.Logger) ([]DeviceInfo, error) {
	backend := platformBackend()

	ctx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audio context: %w", err)
	}
	defer func() {
		// Uninit releases backend resources (ALSA/WASAPI/CoreAudio handles).
		if uninitErr := ctx.Uninit(); uninitErr != nil {
			log.Error("failed to uninitialize audio context", logger.Error(uninitErr))
		}
		// Free releases the C heap memory allocated by ma_malloc in InitContext.
		ctx.Free()
	}()

	infos, err := ctx.Devices(malgo.Capture)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate capture devices: %w", err)
	}

	devices := make([]DeviceInfo, 0, len(infos))
	seenNames := make(map[string]bool, len(infos))

	for i := range infos {
		name := infos[i].Name()
		if strings.Contains(name, "Discard all samples") {
			continue
		}

		decodedID, err := hexToASCII(infos[i].ID.String())
		if err != nil {
			log.Error("error decoding device ID",
				logger.Int("device_index", i),
				logger.Error(err))
			continue
		}

		// ALSA enumerates multiple pseudo-devices per card; deduplicate by name.
		if seenNames[name] {
			continue
		}
		seenNames[name] = true

		devices = append(devices, DeviceInfo{
			Index: i,
			Name:  name,
			ID:    decodedID,
		})
	}

	return devices, nil
}

// isDefaultDeviceToken reports whether deviceID is one of the well-known
// tokens that refer to the platform's default audio device.
func isDefaultDeviceToken(deviceID string) bool {
	return deviceID == DeviceIDSysDefault || deviceID == DeviceIDDefault
}

// matchesDevice reports whether the device identified by (decodedID, info)
// should be selected for the requested deviceID string.
//
// On Windows and macOS the DeviceIDDefault / DeviceIDSysDefault token selects
// the system default device. Otherwise, either a substring of the name or an
// exact ID match is accepted.
func matchesDevice(decodedID string, info *malgo.DeviceInfo, deviceID string) bool {
	if (runtime.GOOS == captureOSWindows || runtime.GOOS == captureOSDarwin) &&
		isDefaultDeviceToken(deviceID) {
		return info.IsDefault == 1
	}
	return decodedID == deviceID || strings.Contains(info.Name(), deviceID)
}

// uninitAndFreeContext performs the two-step malgo context teardown:
// Uninit releases backend resources, Free releases the C heap allocation.
// This must be called for every AllocatedContext returned by malgo.InitContext.
func uninitAndFreeContext(ctx *malgo.AllocatedContext, log logger.Logger) {
	if uninitErr := ctx.Uninit(); uninitErr != nil {
		log.Error("failed to uninitialize malgo context", logger.Error(uninitErr))
	}
	ctx.Free()
}

// startCapture locates the requested device, initialises a malgo context and
// device, and starts the capture goroutine. It returns the DeviceInfo for the
// selected device and a done channel that is closed when the capture goroutine
// exits.
//
// malgo cleanup strategy: Device.Uninit() calls both ma_device_uninit and
// ma_free internally, so a single call suffices. AllocatedContext requires
// Uninit() (ma_context_uninit) followed by Free() (ma_free) — see
// uninitAndFreeContext.
//
// The capture goroutine will be stopped when ctx is cancelled.
func startCapture(
	ctx context.Context,
	sourceID string,
	deviceID string,
	cfg DeviceConfig,
	dispatcher AudioDispatcher,
	log logger.Logger,
) (DeviceInfo, chan struct{}, error) {

	backend := platformBackend()

	malgoCtx, err := malgo.InitContext([]malgo.Backend{backend}, malgo.ContextConfig{}, nil)
	if err != nil {
		return DeviceInfo{}, nil, errors.New(err).
			Component("audiocore.capture").
			Category(errors.CategoryAudioSource).
			Context("operation", "audio_context_init").
			Context("source_id", sourceID).
			Build()
	}

	infos, err := malgoCtx.Devices(malgo.Capture)
	if err != nil {
		uninitAndFreeContext(malgoCtx, log)
		return DeviceInfo{}, nil, errors.New(err).
			Component("audiocore.capture").
			Category(errors.CategoryAudioSource).
			Context("operation", "enumerate_capture_devices").
			Context("source_id", sourceID).
			Build()
	}

	// Find the device matching deviceID.
	var selectedInfo *malgo.DeviceInfo
	var selectedDevInfo DeviceInfo
	log.Info("enumerating capture devices",
		logger.String("source_id", sourceID),
		logger.String("requested_device", deviceID),
		logger.Int("device_count", len(infos)))
	for i := range infos {
		decodedID, decErr := hexToASCII(infos[i].ID.String())
		if decErr != nil {
			log.Warn("failed to decode device ID",
				logger.Int("device_index", i),
				logger.Error(decErr))
			continue
		}
		log.Debug("found capture device",
			logger.Int("index", i),
			logger.String("name", infos[i].Name()),
			logger.String("decoded_id", decodedID))
		if matchesDevice(decodedID, &infos[i], deviceID) {
			selectedInfo = &infos[i]
			selectedDevInfo = DeviceInfo{
				Index: i,
				Name:  infos[i].Name(),
				ID:    decodedID,
			}
			break
		}
	}

	if selectedInfo == nil {
		uninitAndFreeContext(malgoCtx, log)
		return DeviceInfo{}, nil, errors.Newf("no device found matching %q", deviceID).
			Component("audiocore.capture").
			Category(errors.CategoryAudioSource).
			Context("operation", "find_device").
			Context("source_id", sourceID).
			Context("device_id", deviceID).
			Build()
	}

	deviceCfg := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceCfg.Capture.Channels = uint32(cfg.Channels)
	deviceCfg.SampleRate = uint32(cfg.SampleRate)
	deviceCfg.Alsa.NoMMap = 1
	deviceCfg.Capture.DeviceID = selectedInfo.ID.Pointer()

	var captureDevice *malgo.Device
	var formatType malgo.FormatType

	onReceiveFrames := func(_, pSamples []byte, _ uint32) {
		if len(pSamples) == 0 {
			return
		}

		// Convert non-S16 formats to S16 so the rest of the pipeline
		// (capture buffer, BirdNET analysis) receives consistent 16-bit PCM.
		// Devices like the Scarlett 2i2 capture at 32-bit natively.
		data, bitDepth := convertToS16IfNeeded(pSamples, formatType, cfg.BitDepth)

		frame := AudioFrame{
			SourceID:   sourceID,
			SourceName: selectedDevInfo.Name,
			Data:       data,
			SampleRate: cfg.SampleRate,
			BitDepth:   bitDepth,
			Channels:   cfg.Channels,
			Timestamp:  time.Now(),
		}

		dispatcher.Dispatch(frame)
	}

	callbacks := malgo.DeviceCallbacks{
		Data: onReceiveFrames,
	}

	captureDevice, err = malgo.InitDevice(malgoCtx.Context, deviceCfg, callbacks)
	if err != nil {
		uninitAndFreeContext(malgoCtx, log)
		return DeviceInfo{}, nil, errors.New(err).
			Component("audiocore.capture").
			Category(errors.CategoryAudioSource).
			Context("operation", "device_init").
			Context("source_id", sourceID).
			Context("device_name", selectedDevInfo.Name).
			Build()
	}

	// Capture the actual format reported by the device after init.
	formatType = captureDevice.CaptureFormat()

	if err = captureDevice.Start(); err != nil {
		// Device.Uninit() handles both ma_device_uninit and ma_free internally.
		captureDevice.Uninit()
		uninitAndFreeContext(malgoCtx, log)
		return DeviceInfo{}, nil, errors.New(err).
			Component("audiocore.capture").
			Category(errors.CategoryAudioSource).
			Context("operation", "device_start").
			Context("source_id", sourceID).
			Context("device_name", selectedDevInfo.Name).
			Build()
	}

	log.Info("malgo capture device started",
		logger.String("source_id", sourceID),
		logger.String("device", selectedDevInfo.Name),
		logger.Int("sample_rate", cfg.SampleRate),
		logger.Int("channels", cfg.Channels))

	// done is closed when the capture goroutine exits, allowing callers to
	// wait for graceful device teardown.
	done := make(chan struct{})

	// Background goroutine that owns device and context lifetime.
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				panicErr := fmt.Errorf("panic in capture goroutine: %v", r)
				log.Error("panic in capture goroutine",
					logger.String("source_id", sourceID),
					logger.Any("panic", r))
				_ = errors.New(panicErr).
					Component("audiocore.capture").
					Category(errors.CategoryAudio).
					Context("operation", "capture_goroutine_panic").
					Context("source_id", sourceID).
					Priority(errors.PriorityCritical).
					Build()
			}
		}()
		defer func() {
			// Device.Uninit() calls both ma_device_uninit and ma_free.
			if err := captureDevice.Stop(); err != nil {
				log.Error("failed to stop capture device",
					logger.String("source_id", sourceID),
					logger.Error(err))
			}
			captureDevice.Uninit()
			// Context requires explicit two-step teardown.
			uninitAndFreeContext(malgoCtx, log)
			log.Info("malgo capture device stopped",
				logger.String("source_id", sourceID),
				logger.String("device", selectedDevInfo.Name))
		}()

		<-ctx.Done()
	}()

	return selectedDevInfo, done, nil
}

// formatBitDepth returns the bit depth implied by the malgo format type,
// falling back to the requested bit depth when the format is not recognised.
func formatBitDepth(format malgo.FormatType, requested int) int {
	switch format {
	case malgo.FormatU8:
		return 8
	case malgo.FormatS16:
		return 16
	case malgo.FormatS24:
		return 24
	case malgo.FormatS32:
		return 32
	case malgo.FormatF32:
		return 32
	default:
		return requested
	}
}

// convertToS16IfNeeded converts audio samples from S24, S32, or F32 format to
// S16 (16-bit signed PCM). If the source is already S16 or U8, the data is
// copied as-is. This ensures the capture buffer and downstream pipeline always
// receive consistent 16-bit PCM regardless of the hardware's native format.
func convertToS16IfNeeded(samples []byte, format malgo.FormatType, requestedBitDepth int) (data []byte, bitDepth int) {
	switch format {
	case malgo.FormatS16:
		// Already 16-bit — just copy.
		out := make([]byte, len(samples))
		copy(out, samples)
		return out, 16

	case malgo.FormatS24:
		return convertS24ToS16(samples), 16

	case malgo.FormatS32:
		return convertS32ToS16(samples), 16

	case malgo.FormatF32:
		return convertF32ToS16(samples), 16

	default:
		// U8 or unknown — copy as-is with original bit depth.
		out := make([]byte, len(samples))
		copy(out, samples)
		return out, formatBitDepth(format, requestedBitDepth)
	}
}

// convertS24ToS16 converts 24-bit signed integer PCM to 16-bit signed PCM.
func convertS24ToS16(samples []byte) []byte {
	const srcBytes = 3
	sampleCount := len(samples) / srcBytes
	out := make([]byte, sampleCount*2)

	for i := range sampleCount {
		srcIdx := i * srcBytes
		dstIdx := i * 2

		// Read 24-bit little-endian, sign-extend to 32-bit.
		val := int32(samples[srcIdx]) | int32(samples[srcIdx+1])<<8 | int32(samples[srcIdx+2])<<16
		if val&0x800000 != 0 {
			val |= -0x1000000
		}
		// Shift right 8 bits to get 16-bit range.
		val >>= 8
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		binary.LittleEndian.PutUint16(out[dstIdx:dstIdx+2], uint16(val)) //nolint:gosec // G115: val clamped to 16-bit range
	}
	return out
}

// convertS32ToS16 converts 32-bit signed integer PCM to 16-bit signed PCM.
func convertS32ToS16(samples []byte) []byte {
	const srcBytes = 4
	sampleCount := len(samples) / srcBytes
	out := make([]byte, sampleCount*2)

	for i := range sampleCount {
		srcIdx := i * srcBytes
		dstIdx := i * 2

		val := int32(binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+srcBytes])) //nolint:gosec // G115: 32→32 bit
		val >>= 16
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		binary.LittleEndian.PutUint16(out[dstIdx:dstIdx+2], uint16(val)) //nolint:gosec // G115: val clamped
	}
	return out
}

// convertF32ToS16 converts 32-bit float PCM [-1.0, 1.0] to 16-bit signed PCM.
func convertF32ToS16(samples []byte) []byte {
	const srcBytes = 4
	sampleCount := len(samples) / srcBytes
	out := make([]byte, sampleCount*2)

	for i := range sampleCount {
		srcIdx := i * srcBytes
		dstIdx := i * 2

		bits := binary.LittleEndian.Uint32(samples[srcIdx : srcIdx+srcBytes])
		val := math.Float32frombits(bits)
		val *= 32767.0
		if val > 32767.0 {
			val = 32767.0
		} else if val < -32768.0 {
			val = -32768.0
		}
		binary.LittleEndian.PutUint16(out[dstIdx:dstIdx+2], uint16(int16(val))) //nolint:gosec // G115: val clamped
	}
	return out
}
